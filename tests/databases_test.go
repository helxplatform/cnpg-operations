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

func fakeDatabase(clusterName, dbName string) map[string]interface{} {
	crdName := clusterName + "-" + dbName
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      crdName,
			"namespace": "default",
			"labels": map[string]interface{}{
				"cnpg.io/cluster":  clusterName,
				"cnpg.io/database": dbName,
			},
		},
		"spec": map[string]interface{}{
			"name":                  dbName,
			"owner":                 "appuser",
			"ensure":                "present",
			"databaseReclaimPolicy": "retain",
			"cluster": map[string]interface{}{
				"name": clusterName,
			},
		},
	}
}

// fakeClusterWithRole returns a cluster that already has the given role in spec.managed.roles.
func fakeClusterWithRole(clusterName, namespace, roleName string) map[string]interface{} {
	cl := fakeCluster(clusterName, namespace)
	cl["spec"].(map[string]interface{})["managed"] = map[string]interface{}{
		"roles": []interface{}{
			map[string]interface{}{
				"name":   roleName,
				"ensure": "present",
				"login":  true,
				"passwordSecret": map[string]interface{}{
					"name": "cnpg-" + clusterName + "-user-" + roleName,
				},
			},
		},
	}
	return cl
}

// fakeClusterNoRoles returns a cluster with no managed roles.
func fakeClusterNoRoles(clusterName, namespace string) map[string]interface{} {
	return fakeCluster(clusterName, namespace)
}

func setupDatabaseRouter(m *MockKubeClient) *gin.Engine {
	h := handlers.NewDatabaseHandler(m)
	r := gin.New()
	r.GET("/api/v1/cluster/:name/database", h.ListDatabases)
	r.POST("/api/v1/cluster/:name/database", h.CreateDatabase)
	r.DELETE("/api/v1/cluster/:name/database/:database", h.DeleteDatabase)
	return r
}

// ===== ListDatabases =====

func TestListDatabases_Success(t *testing.T) {
	m := &MockKubeClient{}
	m.On("ListDatabases", mock.Anything, "default", "my-db").
		Return([]map[string]interface{}{fakeDatabase("my-db", "myapp")}, nil)

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster/my-db/database?namespace=default", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DatabasesListResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 1, resp.Count)
	assert.Equal(t, "myapp", resp.Databases[0].DatabaseName)
	assert.Equal(t, "appuser", resp.Databases[0].Owner)
	m.AssertExpectations(t)
}

func TestListDatabases_Empty(t *testing.T) {
	m := &MockKubeClient{}
	m.On("ListDatabases", mock.Anything, "default", "my-db").
		Return([]map[string]interface{}{}, nil)

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/cluster/my-db/database?namespace=default", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DatabasesListResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Count)
	m.AssertExpectations(t)
}

// ===== CreateDatabase =====

func TestCreateDatabase_Success(t *testing.T) {
	m := &MockKubeClient{}
	// ensureOwnerRole: owner already exists in cluster
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeClusterWithRole("my-db", "default", "appuser"), nil)
	m.On("CreateDatabase", mock.Anything, "default", mock.Anything).Return(nil)

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName:  "myapp",
		Owner:         "appuser",
		ReclaimPolicy: "retain",
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp models.DatabaseResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "myapp", resp.Database.DatabaseName)
	assert.Equal(t, "my-db-myapp", resp.Database.CRDName)
	assert.Equal(t, "retain", resp.Database.ReclaimPolicy)
	assert.NotContains(t, resp.Message, "auto-created")
	m.AssertExpectations(t)
}

func TestCreateDatabase_AutoCreateOwnerRole(t *testing.T) {
	m := &MockKubeClient{}
	// ensureOwnerRole: owner does NOT exist, must be auto-created
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeClusterNoRoles("my-db", "default"), nil)
	m.On("CreateSecret", mock.Anything, "default", "cnpg-my-db-user-appuser", "appuser", mock.AnythingOfType("string"), mock.Anything).
		Return(nil)
	m.On("UpdateCluster", mock.Anything, "default", "my-db", mock.Anything).
		Return(fakeClusterWithRole("my-db", "default", "appuser"), nil)
	m.On("CreateDatabase", mock.Anything, "default", mock.Anything).Return(nil)

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName:  "myapp",
		Owner:         "appuser",
		ReclaimPolicy: "retain",
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp models.DatabaseResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "auto-created")
	assert.Contains(t, resp.Message, "cnpg-my-db-user-appuser")
	m.AssertExpectations(t)
}

func TestCreateDatabase_DefaultReclaimPolicy(t *testing.T) {
	m := &MockKubeClient{}
	// ensureOwnerRole: owner already exists
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeClusterWithRole("my-db", "default", "appuser"), nil)
	m.On("CreateDatabase", mock.Anything, "default", mock.Anything).Return(nil)

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName: "myapp",
		Owner:        "appuser",
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp models.DatabaseResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "retain", resp.Database.ReclaimPolicy)
	m.AssertExpectations(t)
}

func TestCreateDatabase_InvalidReclaimPolicy(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName:  "myapp",
		Owner:         "appuser",
		ReclaimPolicy: "invalid",
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateDatabase_MissingRequired(t *testing.T) {
	m := &MockKubeClient{}

	body, _ := json.Marshal(map[string]interface{}{"database_name": "myapp"})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateDatabase_InvalidName(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName: "INVALID_NAME",
		Owner:        "appuser",
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateDatabase_DryRun(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDefaultNamespace").Return("default")

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName: "myapp",
		Owner:        "appuser",
		DryRun:       true,
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
}

func TestCreateDatabase_AlreadyExists(t *testing.T) {
	m := &MockKubeClient{}
	// ensureOwnerRole: owner already exists
	m.On("GetCluster", mock.Anything, "default", "my-db").
		Return(fakeClusterWithRole("my-db", "default", "appuser"), nil)
	m.On("CreateDatabase", mock.Anything, "default", mock.Anything).
		Return(k8serrors.NewAlreadyExists(schema.GroupResource{}, "my-db-myapp"))

	body, _ := json.Marshal(models.CreateDatabaseRequest{
		DatabaseName: "myapp",
		Owner:        "appuser",
	})

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/cluster/my-db/database?namespace=default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	m.AssertExpectations(t)
}

// ===== DeleteDatabase =====

func TestDeleteDatabase_RetainPolicy(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDatabase", mock.Anything, "default", "my-db-myapp").
		Return(fakeDatabase("my-db", "myapp"), nil)
	m.On("DeleteDatabase", mock.Anything, "default", "my-db-myapp").Return(nil)

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/my-db/database/myapp?namespace=default", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.MessageResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Contains(t, resp.Message, "retained")
	m.AssertExpectations(t)
}

func TestDeleteDatabase_DeletePolicy(t *testing.T) {
	db := fakeDatabase("my-db", "myapp")
	db["spec"].(map[string]interface{})["databaseReclaimPolicy"] = "delete"

	m := &MockKubeClient{}
	m.On("GetDatabase", mock.Anything, "default", "my-db-myapp").Return(db, nil)
	m.On("DeleteDatabase", mock.Anything, "default", "my-db-myapp").Return(nil)

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/my-db/database/myapp?namespace=default", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.MessageResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp.Message, "DROPPED")
	m.AssertExpectations(t)
}

func TestDeleteDatabase_DryRun(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDatabase", mock.Anything, "default", "my-db-myapp").
		Return(fakeDatabase("my-db", "myapp"), nil)

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/my-db/database/myapp?namespace=default&dry_run=true", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
	m.AssertExpectations(t)
}

func TestDeleteDatabase_NotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetDatabase", mock.Anything, "default", "my-db-missing").
		Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "my-db-missing"))

	r := setupDatabaseRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/cluster/my-db/database/missing?namespace=default", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}
