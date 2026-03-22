package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cnpg-operations/cnpg-rest-server/internal/handlers"
	"github.com/cnpg-operations/cnpg-rest-server/internal/models"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// fakeCluster returns a minimal unstructured cluster object for test use.
func fakeCluster(name, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":            name,
			"namespace":       namespace,
			"resourceVersion": "12345",
		},
		"spec": map[string]interface{}{
			"instances": float64(3),
			"imageName": "ghcr.io/cloudnative-pg/postgresql:16",
			"storage": map[string]interface{}{
				"size": "10Gi",
			},
		},
		"status": map[string]interface{}{
			"phase":          "Cluster in healthy state",
			"readyInstances": float64(3),
			"currentPrimary": name + "-1",
		},
	}
}

func notFoundErr() error {
	return k8serrors.NewNotFound(schema.GroupResource{Group: "postgresql.cnpg.io", Resource: "clusters"}, "not-found")
}

// setupClusterRouter builds a test router wired to the given mock client.
func setupClusterRouter(m *MockKubeClient) *gin.Engine {
	h := handlers.NewClusterHandler(m)
	r := gin.New()
	r.GET("/api/v1/cluster", h.ListClusters)
	r.POST("/api/v1/cluster", h.CreateCluster)
	r.GET("/api/v1/cluster/:name", h.GetCluster)
	r.PATCH("/api/v1/cluster/:name/scale", h.ScaleCluster)
	r.DELETE("/api/v1/cluster/:name", h.DeleteCluster)
	return r
}

// ===== ListClusters =====

func TestListClusters_Success(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")
	m.On("ListClusters", mock.Anything, "default").
		Return([]map[string]interface{}{fakeCluster("test-cluster", "default")}, nil)

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.ClustersListResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 1, resp.Count)
	assert.Equal(t, "test-cluster", resp.Clusters[0].Name)
	m.AssertExpectations(t)
}

func TestListClusters_WithNamespace(t *testing.T) {
	m := &MockKubeClient{}
	m.On("ListClusters", mock.Anything, "production").
		Return([]map[string]interface{}{}, nil)

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster?namespace=production", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	m.AssertExpectations(t)
}

func TestListClusters_K8sError(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")
	m.On("ListClusters", mock.Anything, "default").
		Return(nil, k8serrors.NewForbidden(schema.GroupResource{}, "", nil))

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster", nil))

	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp models.ErrorResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	m.AssertExpectations(t)
}

// ===== GetCluster =====

func TestGetCluster_Success(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeCluster("my-db", "default"), nil)

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster/my-db?namespace=default", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.ClusterResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "my-db", resp.Cluster.Name)
	assert.Equal(t, "Cluster in healthy state", resp.Cluster.Phase)
	m.AssertExpectations(t)
}

func TestGetCluster_NotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "missing").
		Return(nil, notFoundErr())

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster/missing?namespace=default", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}

// ===== CreateCluster =====

func TestCreateCluster_Success(t *testing.T) {
	m := &MockKubeClient{}
	// Namespace is supplied in the request body, so GetDefaultNamespace must NOT be called.
	m.On("CreateCluster", mock.Anything, mock.AnythingOfType("map[string]interface {}")).
		Return(fakeCluster("new-db", "default"), nil)

	body, _ := json.Marshal(models.CreateClusterRequest{
		Name:            "new-db",
		Instances:       3,
		StorageSize:     "20Gi",
		PostgresVersion: "16",
		Namespace:       "default",
	})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp models.ClusterCreateResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "new-db", resp.Name)
	m.AssertExpectations(t)
}

func TestCreateCluster_MissingName(t *testing.T) {
	m := &MockKubeClient{}

	body, _ := json.Marshal(map[string]interface{}{"instances": 3})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateCluster_InvalidName(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")

	body, _ := json.Marshal(models.CreateClusterRequest{Name: "INVALID_NAME"})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateCluster_DryRun(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")

	body, _ := json.Marshal(models.CreateClusterRequest{Name: "dry-db", DryRun: true})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
}

func TestCreateCluster_AlreadyExists(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")
	m.On("CreateCluster", mock.Anything, mock.Anything).
		Return(nil, k8serrors.NewAlreadyExists(schema.GroupResource{}, "my-db"))

	body, _ := json.Marshal(models.CreateClusterRequest{Name: "my-db"})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	m.AssertExpectations(t)
}

// ===== ScaleCluster =====

func TestScaleCluster_Success(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeCluster("my-db", "default"), nil)
	m.On("UpdateCluster", mock.Anything, "default", "my-db", mock.Anything).
		Return(fakeCluster("my-db", "default"), nil)

	body, _ := json.Marshal(models.ScaleClusterRequest{Instances: 5})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/cluster/my-db/scale?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.MessageResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	m.AssertExpectations(t)
}

func TestScaleCluster_DryRun(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeCluster("my-db", "default"), nil)

	body, _ := json.Marshal(models.ScaleClusterRequest{Instances: 5, DryRun: true})

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/cluster/my-db/scale?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
	m.AssertExpectations(t)
}

// ===== DeleteCluster =====

func TestDeleteCluster_DryRun(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeCluster("my-db", "default"), nil)
	m.On("ListDatabases", mock.Anything, "default", "my-db").
		Return([]map[string]interface{}{}, nil)
	m.On("ListSecretsByLabel", mock.Anything, "default", "cnpg.io/cluster=my-db").
		Return(nil, nil)

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/my-db?namespace=default&dry_run=true", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
	assert.NotEmpty(t, resp.Preview)
	assert.Contains(t, resp.Preview[0], "DELETE Cluster")
	m.AssertExpectations(t)
}

func TestDeleteCluster_Success(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeCluster("my-db", "default"), nil)
	m.On("DeleteCluster", mock.Anything, "default", "my-db").Return(nil)
	m.On("DeleteSecretsByLabel", mock.Anything, "default", "cnpg.io/cluster=my-db").Return(2, nil)

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/my-db?namespace=default", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.MessageResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "2 associated secret(s)")
	m.AssertExpectations(t)
}

func TestDeleteCluster_NotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "missing").
		Return(nil, notFoundErr())

	r := setupClusterRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/missing?namespace=default", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}
