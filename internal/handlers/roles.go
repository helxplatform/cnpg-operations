package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cnpg-operations/cnpg-rest-server/internal/k8s"
	"github.com/cnpg-operations/cnpg-rest-server/internal/models"
)

// RoleHandler handles HTTP requests for PostgreSQL role operations.
type RoleHandler struct {
	client k8s.KubeClient
}

// NewRoleHandler creates a RoleHandler with the given KubeClient.
func NewRoleHandler(client k8s.KubeClient) *RoleHandler {
	return &RoleHandler{client: client}
}

// roleSecretName returns the convention-based secret name for a role.
func roleSecretName(clusterName, roleName string) string {
	return fmt.Sprintf("cnpg-%s-user-%s", clusterName, roleName)
}

// roleInfoFromMap converts an unstructured role map to RoleInfo.
func roleInfoFromMap(role map[string]interface{}) models.RoleInfo {
	info := models.RoleInfo{
		Name:        nestedStr(role, "name"),
		Ensure:      nestedStr(role, "ensure"),
		Login:       nestedBool(role, "login"),
		Superuser:   nestedBool(role, "superuser"),
		Inherit:     nestedBool(role, "inherit"),
		CreateDB:    nestedBool(role, "createdb"),
		CreateRole:  nestedBool(role, "createrole"),
		Replication: nestedBool(role, "replication"),
	}
	if info.Ensure == "" {
		info.Ensure = "present"
	}
	info.PasswordSecret = nestedStr(role, "passwordSecret", "name")

	if inRoles, ok := role["inRoles"].([]interface{}); ok {
		for _, r := range inRoles {
			if s, ok := r.(string); ok {
				info.InRoles = append(info.InRoles, s)
			}
		}
	}
	return info
}

// ListRoles godoc
// @Summary      List PostgreSQL roles
// @Description  List all roles managed in a CloudNativePG cluster via spec.managed.roles
// @Tags         roles
// @Produce      json
// @Param        name       path   string  true   "Cluster name"
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Success      200  {object}  models.RolesListResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/role [get]
func (h *RoleHandler) ListRoles(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")

	obj, err := h.client.GetCluster(c.Request.Context(), namespace, clusterName)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	rawRoles := nestedSlice(obj, "spec", "managed", "roles")
	roles := make([]models.RoleInfo, 0, len(rawRoles))
	for _, r := range rawRoles {
		if roleMap, ok := r.(map[string]interface{}); ok {
			roles = append(roles, roleInfoFromMap(roleMap))
		}
	}

	c.JSON(http.StatusOK, models.RolesListResponse{
		Success: true,
		Cluster: fmt.Sprintf("%s/%s", namespace, clusterName),
		Roles:   roles,
		Count:   len(roles),
	})
}

// CreateRole godoc
// @Summary      Create a PostgreSQL role
// @Description  Create a new role in a CloudNativePG cluster. Auto-generates a password stored in a Kubernetes secret.
// @Tags         roles
// @Accept       json
// @Produce      json
// @Param        name       path   string                    true   "Cluster name"
// @Param        namespace  query  string                    false  "Kubernetes namespace (defaults to server namespace)"
// @Param        request    body   models.CreateRoleRequest  true   "Role configuration"
// @Success      201  {object}  models.RoleResponse
// @Success      200  {object}  models.DryRunResponse  "dry_run=true"
// @Failure      400  {object}  models.ErrorResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      409  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/role [post]
func (h *RoleHandler) CreateRole(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")

	var req models.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	if err := k8s.ValidateRFC1123(req.RoleName, "Role"); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	obj, err := h.client.GetCluster(c.Request.Context(), namespace, clusterName)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	// Check role doesn't already exist
	rawRoles := nestedSlice(obj, "spec", "managed", "roles")
	for _, r := range rawRoles {
		if roleMap, ok := r.(map[string]interface{}); ok {
			if nestedStr(roleMap, "name") == req.RoleName {
				respondError(c, http.StatusConflict, fmt.Errorf("role %q already exists in cluster %q/%q", req.RoleName, namespace, clusterName))
				return
			}
		}
	}

	secretName := roleSecretName(clusterName, req.RoleName)
	if err := k8s.ValidateRFC1123(secretName, "Role secret"); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	if req.DryRun {
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: role %q would be created in cluster %q/%q", req.RoleName, namespace, clusterName),
			Preview: []string{
				fmt.Sprintf("CREATE Role %q in cluster %q/%q (login=%v, superuser=%v, createdb=%v, createrole=%v, replication=%v)",
					req.RoleName, namespace, clusterName, req.Login, req.Superuser, req.CreateDB, req.CreateRole, req.Replication),
				fmt.Sprintf("CREATE Secret %q (auto-generated password)", secretName),
			},
		})
		return
	}

	// Generate password and create secret
	password, err := k8s.GeneratePassword(16)
	if err != nil {
		respondError(c, http.StatusInternalServerError, fmt.Errorf("failed to generate password: %w", err))
		return
	}

	if err := h.client.CreateSecret(c.Request.Context(), namespace, secretName, req.RoleName, password, map[string]string{
		"app.kubernetes.io/name": "cnpg",
		"cnpg.io/cluster":        clusterName,
		"cnpg.io/role":           req.RoleName,
	}); err != nil {
		respondError(c, k8sStatus(err), fmt.Errorf("failed to create password secret: %w", err))
		return
	}

	// Append role to cluster spec
	newRole := map[string]interface{}{
		"name":        req.RoleName,
		"ensure":      "present",
		"login":       req.Login,
		"superuser":   req.Superuser,
		"inherit":     req.Inherit,
		"createdb":    req.CreateDB,
		"createrole":  req.CreateRole,
		"replication": req.Replication,
		"passwordSecret": map[string]interface{}{
			"name": secretName,
		},
	}

	obj = ensureManagedRoles(obj)
	obj["spec"].(map[string]interface{})["managed"].(map[string]interface{})["roles"] = append(rawRoles, newRole)

	if _, err := h.client.UpdateCluster(c.Request.Context(), namespace, clusterName, obj); err != nil {
		// Best-effort cleanup of the secret we just created
		_ = h.client.DeleteSecret(c.Request.Context(), namespace, secretName)
		respondError(c, k8sStatus(err), err)
		return
	}

	c.JSON(http.StatusCreated, models.RoleResponse{
		Success:  true,
		Role:     roleInfoFromMap(newRole),
		Message:  fmt.Sprintf("password stored in secret %q; retrieve with: kubectl get secret %s -n %s -o jsonpath='{.data.password}' | base64 -d", secretName, secretName, namespace),
	})
}

// UpdateRole godoc
// @Summary      Update a PostgreSQL role
// @Description  Update attributes and/or password of an existing role in a CloudNativePG cluster
// @Tags         roles
// @Accept       json
// @Produce      json
// @Param        name       path   string                    true   "Cluster name"
// @Param        role       path   string                    true   "Role name"
// @Param        namespace  query  string                    false  "Kubernetes namespace (defaults to server namespace)"
// @Param        request    body   models.UpdateRoleRequest  true   "Role update (all fields optional)"
// @Success      200  {object}  models.RoleResponse
// @Failure      400  {object}  models.ErrorResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/role/{role} [put]
func (h *RoleHandler) UpdateRole(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")
	roleName := c.Param("role")

	var req models.UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	obj, err := h.client.GetCluster(c.Request.Context(), namespace, clusterName)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	rawRoles := nestedSlice(obj, "spec", "managed", "roles")
	roleIdx := -1
	for i, r := range rawRoles {
		if roleMap, ok := r.(map[string]interface{}); ok {
			if nestedStr(roleMap, "name") == roleName {
				roleIdx = i
				break
			}
		}
	}

	if roleIdx == -1 {
		respondError(c, http.StatusNotFound, fmt.Errorf("role %q not found in cluster %q/%q", roleName, namespace, clusterName))
		return
	}

	roleMap := rawRoles[roleIdx].(map[string]interface{})

	// Build dry-run preview
	if req.DryRun {
		changes := collectRoleChanges(roleMap, req)
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: role %q in cluster %q/%q would be updated", roleName, namespace, clusterName),
			Preview: changes,
		})
		return
	}

	// Apply attribute updates
	if req.Login != nil {
		roleMap["login"] = *req.Login
	}
	if req.Superuser != nil {
		roleMap["superuser"] = *req.Superuser
	}
	if req.Inherit != nil {
		roleMap["inherit"] = *req.Inherit
	}
	if req.CreateDB != nil {
		roleMap["createdb"] = *req.CreateDB
	}
	if req.CreateRole != nil {
		roleMap["createrole"] = *req.CreateRole
	}
	if req.Replication != nil {
		roleMap["replication"] = *req.Replication
	}

	// Update password in secret if provided
	if req.Password != nil {
		secretName := roleSecretName(clusterName, roleName)
		if err := h.client.UpdateSecretPassword(c.Request.Context(), namespace, secretName, *req.Password); err != nil {
			respondError(c, k8sStatus(err), fmt.Errorf("failed to update password secret: %w", err))
			return
		}
	}

	rawRoles[roleIdx] = roleMap
	obj["spec"].(map[string]interface{})["managed"].(map[string]interface{})["roles"] = rawRoles

	if _, err := h.client.UpdateCluster(c.Request.Context(), namespace, clusterName, obj); err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	c.JSON(http.StatusOK, models.RoleResponse{
		Success: true,
		Role:    roleInfoFromMap(roleMap),
		Message: fmt.Sprintf("role %q updated in cluster %q/%q", roleName, namespace, clusterName),
	})
}

// DeleteRole godoc
// @Summary      Delete a PostgreSQL role
// @Description  Remove a role from a CloudNativePG cluster and delete its associated Kubernetes secret
// @Tags         roles
// @Produce      json
// @Param        name       path   string  true   "Cluster name"
// @Param        role       path   string  true   "Role name"
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Param        dry_run    query  bool    false  "Preview deletion without executing"
// @Success      200  {object}  models.MessageResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/role/{role} [delete]
func (h *RoleHandler) DeleteRole(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")
	roleName := c.Param("role")
	dryRun := c.Query("dry_run") == "true"

	obj, err := h.client.GetCluster(c.Request.Context(), namespace, clusterName)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	rawRoles := nestedSlice(obj, "spec", "managed", "roles")
	roleIdx := -1
	for i, r := range rawRoles {
		if roleMap, ok := r.(map[string]interface{}); ok {
			if nestedStr(roleMap, "name") == roleName {
				roleIdx = i
				break
			}
		}
	}

	if roleIdx == -1 {
		respondError(c, http.StatusNotFound, fmt.Errorf("role %q not found in cluster %q/%q", roleName, namespace, clusterName))
		return
	}

	secretName := roleSecretName(clusterName, roleName)

	if dryRun {
		secretExists := h.client.SecretExists(c.Request.Context(), namespace, secretName)
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: role %q would be deleted from cluster %q/%q", roleName, namespace, clusterName),
			Preview: []string{
				fmt.Sprintf("DELETE Role %q from cluster spec", roleName),
				fmt.Sprintf("DELETE Secret %q (exists=%v)", secretName, secretExists),
			},
		})
		return
	}

	// Remove role from slice
	newRoles := append(rawRoles[:roleIdx], rawRoles[roleIdx+1:]...)
	obj["spec"].(map[string]interface{})["managed"].(map[string]interface{})["roles"] = newRoles

	if _, err := h.client.UpdateCluster(c.Request.Context(), namespace, clusterName, obj); err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	// Clean up password secret
	_ = h.client.DeleteSecret(c.Request.Context(), namespace, secretName)

	c.JSON(http.StatusOK, models.MessageResponse{
		Success: true,
		Message: fmt.Sprintf("role %q deleted from cluster %q/%q and associated secret removed", roleName, namespace, clusterName),
	})
}

// ensureManagedRoles guarantees spec.managed.roles exists in the cluster object.
func ensureManagedRoles(obj map[string]interface{}) map[string]interface{} {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		spec = map[string]interface{}{}
		obj["spec"] = spec
	}
	managed, ok := spec["managed"].(map[string]interface{})
	if !ok {
		managed = map[string]interface{}{}
		spec["managed"] = managed
	}
	if _, ok := managed["roles"]; !ok {
		managed["roles"] = []interface{}{}
	}
	return obj
}

// collectRoleChanges builds a list of human-readable changes for dry-run updates.
func collectRoleChanges(roleMap map[string]interface{}, req models.UpdateRoleRequest) []string {
	var changes []string
	if req.Login != nil {
		changes = append(changes, fmt.Sprintf("login: %v → %v", nestedBool(roleMap, "login"), *req.Login))
	}
	if req.Superuser != nil {
		changes = append(changes, fmt.Sprintf("superuser: %v → %v", nestedBool(roleMap, "superuser"), *req.Superuser))
	}
	if req.Inherit != nil {
		changes = append(changes, fmt.Sprintf("inherit: %v → %v", nestedBool(roleMap, "inherit"), *req.Inherit))
	}
	if req.CreateDB != nil {
		changes = append(changes, fmt.Sprintf("createdb: %v → %v", nestedBool(roleMap, "createdb"), *req.CreateDB))
	}
	if req.CreateRole != nil {
		changes = append(changes, fmt.Sprintf("createrole: %v → %v", nestedBool(roleMap, "createrole"), *req.CreateRole))
	}
	if req.Replication != nil {
		changes = append(changes, fmt.Sprintf("replication: %v → %v", nestedBool(roleMap, "replication"), *req.Replication))
	}
	if req.Password != nil {
		changes = append(changes, "password: <updated>")
	}
	if len(changes) == 0 {
		changes = []string{"no changes specified"}
	}
	return changes
}
