package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cnpg-operations/cnpg-rest-server/internal/k8s"
	"github.com/cnpg-operations/cnpg-rest-server/internal/models"
)

// DatabaseHandler handles HTTP requests for PostgreSQL database operations.
type DatabaseHandler struct {
	client k8s.KubeClient
}

// NewDatabaseHandler creates a DatabaseHandler with the given KubeClient.
func NewDatabaseHandler(client k8s.KubeClient) *DatabaseHandler {
	return &DatabaseHandler{client: client}
}

// databaseCRDName returns the convention-based CRD name for a database.
func databaseCRDName(clusterName, databaseName string) string {
	return fmt.Sprintf("%s-%s", clusterName, databaseName)
}

// databaseInfoFromMap converts an unstructured Database CRD object to DatabaseInfo.
func databaseInfoFromMap(obj map[string]interface{}) models.DatabaseInfo {
	return models.DatabaseInfo{
		CRDName:       nestedStr(obj, "metadata", "name"),
		DatabaseName:  nestedStr(obj, "spec", "name"),
		Owner:         nestedStr(obj, "spec", "owner"),
		Ensure:        nestedStr(obj, "spec", "ensure"),
		ReclaimPolicy: nestedStr(obj, "spec", "databaseReclaimPolicy"),
	}
}

// ListDatabases godoc
// @Summary      List PostgreSQL databases
// @Description  List all Database CRDs managed for a CloudNativePG cluster
// @Tags         databases
// @Produce      json
// @Param        name       path   string  true   "Cluster name"
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Success      200  {object}  models.DatabasesListResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/database [get]
func (h *DatabaseHandler) ListDatabases(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")

	dbs, err := h.client.ListDatabases(c.Request.Context(), namespace, clusterName)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	dbInfos := make([]models.DatabaseInfo, len(dbs))
	for i, db := range dbs {
		dbInfos[i] = databaseInfoFromMap(db)
	}

	c.JSON(http.StatusOK, models.DatabasesListResponse{
		Success:   true,
		Cluster:   fmt.Sprintf("%s/%s", namespace, clusterName),
		Databases: dbInfos,
		Count:     len(dbInfos),
	})
}

// CreateDatabase godoc
// @Summary      Create a PostgreSQL database
// @Description  Create a Database CRD that CloudNativePG will reconcile into a PostgreSQL database
// @Tags         databases
// @Accept       json
// @Produce      json
// @Param        name       path   string                        true   "Cluster name"
// @Param        namespace  query  string                        false  "Kubernetes namespace (defaults to server namespace)"
// @Param        request    body   models.CreateDatabaseRequest  true   "Database configuration"
// @Success      201  {object}  models.DatabaseResponse
// @Success      200  {object}  models.DryRunResponse  "dry_run=true"
// @Failure      400  {object}  models.ErrorResponse
// @Failure      409  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/database [post]
func (h *DatabaseHandler) CreateDatabase(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")

	var req models.CreateDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	if err := k8s.ValidateRFC1123(req.DatabaseName, "Database"); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	crdName := databaseCRDName(clusterName, req.DatabaseName)
	if err := k8s.ValidateRFC1123(crdName, "Database CRD"); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	reclaimPolicy := req.ReclaimPolicy
	if reclaimPolicy == "" {
		reclaimPolicy = "retain"
	}
	if reclaimPolicy != "retain" && reclaimPolicy != "delete" {
		respondError(c, http.StatusBadRequest, fmt.Errorf("reclaim_policy must be 'retain' or 'delete', got %q", reclaimPolicy))
		return
	}

	if req.DryRun {
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: database %q would be created in cluster %q/%q", req.DatabaseName, namespace, clusterName),
			Preview: []string{
				fmt.Sprintf("CREATE Database CRD %q (database %q, owner %q, reclaim_policy=%s)", crdName, req.DatabaseName, req.Owner, reclaimPolicy),
				"AUTO-CREATE owner role if missing (with login + password secret)",
			},
		})
		return
	}

	// Ensure the owner role exists (auto-create with login + password if not)
	secretName, roleCreated, err := ensureOwnerRole(c.Request.Context(), h.client, namespace, clusterName, req.Owner)
	if err != nil {
		respondError(c, k8sStatus(err), fmt.Errorf("failed to ensure owner role %q: %w", req.Owner, err))
		return
	}

	spec := buildDatabaseSpec(clusterName, crdName, req.DatabaseName, req.Owner, reclaimPolicy, namespace)

	if err := h.client.CreateDatabase(c.Request.Context(), namespace, spec); err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	msg := fmt.Sprintf("Database CRD %q created; CloudNativePG operator will reconcile the database", crdName)
	if roleCreated {
		msg += fmt.Sprintf("; owner role %q auto-created with password in secret %q", req.Owner, secretName)
	}

	c.JSON(http.StatusCreated, models.DatabaseResponse{
		Success: true,
		Database: models.DatabaseInfo{
			CRDName:       crdName,
			DatabaseName:  req.DatabaseName,
			Owner:         req.Owner,
			Ensure:        "present",
			ReclaimPolicy: reclaimPolicy,
		},
		Message: msg,
	})
}

// DeleteDatabase godoc
// @Summary      Delete a PostgreSQL database
// @Description  Delete the Database CRD for a cluster. Whether the actual database is dropped depends on its reclaimPolicy.
// @Tags         databases
// @Produce      json
// @Param        name       path   string  true   "Cluster name"
// @Param        database   path   string  true   "Database name"
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Param        dry_run    query  bool    false  "Preview deletion without executing"
// @Success      200  {object}  models.MessageResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/database/{database} [delete]
func (h *DatabaseHandler) DeleteDatabase(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	clusterName := c.Param("name")
	databaseName := c.Param("database")
	dryRun := c.Query("dry_run") == "true"

	crdName := databaseCRDName(clusterName, databaseName)

	// Verify database CRD exists and get its reclaim policy
	obj, err := h.client.GetDatabase(c.Request.Context(), namespace, crdName)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	reclaimPolicy := nestedStr(obj, "spec", "databaseReclaimPolicy")
	owner := nestedStr(obj, "spec", "owner")

	if dryRun {
		action := "retained in PostgreSQL (reclaim_policy=retain)"
		if reclaimPolicy == "delete" {
			action = "DROPPED from PostgreSQL (reclaim_policy=delete)"
		}
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: Database CRD %q would be deleted; database %q will be %s", crdName, databaseName, action),
			Preview: []string{
				fmt.Sprintf("DELETE Database CRD %q (owner=%s, reclaim_policy=%s)", crdName, owner, reclaimPolicy),
			},
		})
		return
	}

	if err := h.client.DeleteDatabase(c.Request.Context(), namespace, crdName); err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	consequence := "database retained in PostgreSQL (reclaim_policy=retain)"
	if reclaimPolicy == "delete" {
		consequence = "database will be DROPPED from PostgreSQL (reclaim_policy=delete)"
	}

	c.JSON(http.StatusOK, models.MessageResponse{
		Success: true,
		Message: fmt.Sprintf("Database CRD %q deleted; %s", crdName, consequence),
	})
}

// buildDatabaseSpec constructs the unstructured Database CRD spec.
func buildDatabaseSpec(clusterName, crdName, databaseName, owner, reclaimPolicy, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Database",
		"metadata": map[string]interface{}{
			"name":      crdName,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"cnpg.io/cluster":  clusterName,
				"cnpg.io/database": databaseName,
			},
		},
		"spec": map[string]interface{}{
			"name":  databaseName,
			"owner": owner,
			"cluster": map[string]interface{}{
				"name": clusterName,
			},
			"ensure":                "present",
			"databaseReclaimPolicy": reclaimPolicy,
		},
	}
}
