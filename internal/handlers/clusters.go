package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cnpg-operations/cnpg-rest-server/internal/k8s"
	"github.com/cnpg-operations/cnpg-rest-server/internal/models"
)

// ClusterHandler handles HTTP requests for cluster operations.
type ClusterHandler struct {
	client k8s.KubeClient
}

// NewClusterHandler creates a ClusterHandler with the given KubeClient.
func NewClusterHandler(client k8s.KubeClient) *ClusterHandler {
	return &ClusterHandler{client: client}
}

// clusterInfoFromMap converts an unstructured cluster object to ClusterInfo.
func clusterInfoFromMap(obj map[string]interface{}) models.ClusterInfo {
	info := models.ClusterInfo{
		Name:            nestedStr(obj, "metadata", "name"),
		Namespace:       nestedStr(obj, "metadata", "namespace"),
		Instances:       nestedInt(obj, "spec", "instances"),
		ReadyInstances:  nestedInt(obj, "status", "readyInstances"),
		Phase:           nestedStr(obj, "status", "phase"),
		CurrentPrimary:  nestedStr(obj, "status", "currentPrimary"),
		PostgresVersion: nestedStr(obj, "spec", "imageName"),
		StorageSize:     nestedStr(obj, "spec", "storage", "size"),
		StorageClass:    nestedStr(obj, "spec", "storage", "storageClass"),
		Conditions:      nestedSlice(obj, "status", "conditions"),
	}
	if pg, ok := obj["spec"].(map[string]interface{}); ok {
		if pgCfg, ok := pg["postgresql"].(map[string]interface{}); ok {
			info.PGParameters = pgCfg["parameters"]
		}
	}
	return info
}

// ListClusters godoc
// @Summary      List PostgreSQL clusters
// @Description  List all CloudNativePG clusters in the given namespace (defaults to server namespace)
// @Tags         clusters
// @Produce      json
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Success      200  {object}  models.ClustersListResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster [get]
func (h *ClusterHandler) ListClusters(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)

	clusters, err := h.client.ListClusters(c.Request.Context(), namespace)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	infos := make([]models.ClusterInfo, len(clusters))
	for i, cl := range clusters {
		infos[i] = clusterInfoFromMap(cl)
	}

	c.JSON(http.StatusOK, models.ClustersListResponse{
		Success:  true,
		Clusters: infos,
		Count:    len(infos),
		Scope:    fmt.Sprintf("namespace %q", namespace),
	})
}

// GetCluster godoc
// @Summary      Get a PostgreSQL cluster
// @Description  Get detailed status for a specific CloudNativePG cluster
// @Tags         clusters
// @Produce      json
// @Param        name       path   string  true   "Cluster name"
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Success      200  {object}  models.ClusterResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name} [get]
func (h *ClusterHandler) GetCluster(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	name := c.Param("name")

	obj, err := h.client.GetCluster(c.Request.Context(), namespace, name)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	c.JSON(http.StatusOK, models.ClusterResponse{
		Success: true,
		Cluster: clusterInfoFromMap(obj),
	})
}

// CreateCluster godoc
// @Summary      Create a PostgreSQL cluster
// @Description  Create a new CloudNativePG PostgreSQL cluster with HA configuration
// @Tags         clusters
// @Accept       json
// @Produce      json
// @Param        request  body  models.CreateClusterRequest  true  "Cluster configuration"
// @Success      201  {object}  models.ClusterCreateResponse
// @Success      200  {object}  models.DryRunResponse  "dry_run=true"
// @Failure      400  {object}  models.ErrorResponse
// @Failure      409  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster [post]
func (h *ClusterHandler) CreateCluster(c *gin.Context) {
	var req models.CreateClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	// Apply defaults
	if req.Instances == 0 {
		req.Instances = 3
	}
	if req.StorageSize == "" {
		req.StorageSize = "10Gi"
	}
	if req.PostgresVersion == "" {
		req.PostgresVersion = "16"
	}
	if req.Namespace == "" {
		req.Namespace = h.client.GetDefaultNamespace()
	}

	if err := k8s.ValidateRFC1123(req.Name, "Cluster"); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	spec := buildClusterSpec(req)

	if req.DryRun {
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: cluster %q in namespace %q would be created", req.Name, req.Namespace),
			Preview: fmt.Sprintf("instances=%d, postgres=%s, storage=%s", req.Instances, req.PostgresVersion, req.StorageSize),
		})
		return
	}

	result, err := h.client.CreateCluster(c.Request.Context(), spec)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	c.JSON(http.StatusCreated, models.ClusterCreateResponse{
		Success:   true,
		Message:   fmt.Sprintf("cluster %q created; monitor with GET /api/v1/cluster/%s?namespace=%s", req.Name, req.Name, req.Namespace),
		Name:      nestedStr(result, "metadata", "name"),
		Namespace: nestedStr(result, "metadata", "namespace"),
	})
}

// ScaleCluster godoc
// @Summary      Scale a PostgreSQL cluster
// @Description  Change the number of instances in a CloudNativePG cluster
// @Tags         clusters
// @Accept       json
// @Produce      json
// @Param        name       path   string                      true   "Cluster name"
// @Param        namespace  query  string                      false  "Kubernetes namespace (defaults to server namespace)"
// @Param        request    body   models.ScaleClusterRequest  true   "Scale configuration"
// @Success      200  {object}  models.MessageResponse
// @Failure      400  {object}  models.ErrorResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name}/scale [patch]
func (h *ClusterHandler) ScaleCluster(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	name := c.Param("name")

	var req models.ScaleClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err)
		return
	}

	obj, err := h.client.GetCluster(c.Request.Context(), namespace, name)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	currentInstances := nestedInt(obj, "spec", "instances")

	if req.DryRun {
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: cluster %q/%q would scale %d → %d instances", namespace, name, currentInstances, req.Instances),
		})
		return
	}

	// Mutate and PUT
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		respondError(c, http.StatusInternalServerError, fmt.Errorf("unexpected cluster spec format"))
		return
	}
	spec["instances"] = req.Instances

	if _, err := h.client.UpdateCluster(c.Request.Context(), namespace, name, obj); err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	c.JSON(http.StatusOK, models.MessageResponse{
		Success: true,
		Message: fmt.Sprintf("cluster %q/%q scaling %d → %d; monitor with GET /api/v1/cluster/%s?namespace=%s", namespace, name, currentInstances, req.Instances, name, namespace),
	})
}

// DeleteCluster godoc
// @Summary      Delete a PostgreSQL cluster
// @Description  Permanently delete a CloudNativePG cluster and its associated role secrets. Requires confirm=true.
// @Tags         clusters
// @Produce      json
// @Param        name       path   string  true   "Cluster name"
// @Param        namespace  query  string  false  "Kubernetes namespace (defaults to server namespace)"
// @Param        confirm    query  bool    false  "Must be true to confirm destructive deletion"
// @Param        dry_run    query  bool    false  "Preview deletion without executing"
// @Success      200  {object}  models.MessageResponse
// @Failure      400  {object}  models.ErrorResponse
// @Failure      404  {object}  models.ErrorResponse
// @Failure      500  {object}  models.ErrorResponse
// @Router       /cluster/{name} [delete]
func (h *ClusterHandler) DeleteCluster(c *gin.Context) {
	namespace := resolveNamespace(c, h.client)
	name := c.Param("name")
	confirm := c.Query("confirm") == "true"
	dryRun := c.Query("dry_run") == "true"

	// Verify cluster exists before proceeding
	obj, err := h.client.GetCluster(c.Request.Context(), namespace, name)
	if err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	if dryRun {
		secrets, _ := h.client.ListSecretsByLabel(c.Request.Context(), namespace, fmt.Sprintf("cnpg.io/cluster=%s", name))
		instances := nestedInt(obj, "spec", "instances")
		storageSize := nestedStr(obj, "spec", "storage", "size")
		c.JSON(http.StatusOK, models.DryRunResponse{
			Success: true,
			DryRun:  true,
			Message: fmt.Sprintf("dry run: cluster %q/%q would be deleted along with %d secret(s)", namespace, name, len(secrets)),
			Preview: fmt.Sprintf("instances=%d, storage=%s per instance; re-run with confirm=true&dry_run=false to execute", instances, storageSize),
		})
		return
	}

	if !confirm {
		respondError(c, http.StatusBadRequest, fmt.Errorf(
			"deletion of cluster %q/%q requires ?confirm=true — this is irreversible and all data will be lost",
			namespace, name,
		))
		return
	}

	if err := h.client.DeleteCluster(c.Request.Context(), namespace, name); err != nil {
		respondError(c, k8sStatus(err), err)
		return
	}

	secretsDeleted, _ := h.client.DeleteSecretsByLabel(
		c.Request.Context(), namespace, fmt.Sprintf("cnpg.io/cluster=%s", name),
	)

	c.JSON(http.StatusOK, models.MessageResponse{
		Success: true,
		Message: fmt.Sprintf("cluster %q/%q deletion initiated; %d associated secret(s) cleaned up", namespace, name, secretsDeleted),
	})
}

// buildClusterSpec constructs the unstructured CNPG Cluster spec for the create API.
func buildClusterSpec(req models.CreateClusterRequest) map[string]interface{} {
	storage := map[string]interface{}{
		"size": req.StorageSize,
	}
	if req.StorageClass != "" {
		storage["storageClass"] = req.StorageClass
	}

	return map[string]interface{}{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata": map[string]interface{}{
			"name":      req.Name,
			"namespace": req.Namespace,
		},
		"spec": map[string]interface{}{
			"instances": req.Instances,
			"imageName": fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%s", req.PostgresVersion),
			"storage":   storage,
			"postgresql": map[string]interface{}{
				"parameters": map[string]interface{}{
					"max_connections": "100",
					"shared_buffers":  "256MB",
				},
			},
		},
	}
}
