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

// fakeClusterWithRoles returns a cluster object that has managed roles.
func fakeClusterWithRoles(name, namespace string, roles []interface{}) map[string]interface{} {
	cl := fakeCluster(name, namespace)
	cl["spec"].(map[string]interface{})["managed"] = map[string]interface{}{
		"roles": roles,
	}
	return cl
}

func fakeRole(roleName string) map[string]interface{} {
	return map[string]interface{}{
		"name":        roleName,
		"ensure":      "present",
		"login":       true,
		"superuser":   false,
		"inherit":     true,
		"createdb":    false,
		"createrole":  false,
		"replication": false,
		"passwordSecret": map[string]interface{}{
			"name": "cnpg-my-db-user-" + roleName,
		},
	}
}

func setupRoleRouter(m *MockKubeClient) *gin.Engine {
	h := handlers.NewRoleHandler(m)
	r := gin.New()
	r.GET("/api/v1/clusters/:namespace/:name/roles", h.ListRoles)
	r.POST("/api/v1/clusters/:namespace/:name/roles", h.CreateRole)
	r.PUT("/api/v1/clusters/:namespace/:name/roles/:role", h.UpdateRole)
	r.DELETE("/api/v1/clusters/:namespace/:name/roles/:role", h.DeleteRole)
	return r
}

// ===== ListRoles =====

func TestListRoles_Success(t *testing.T) {
	m := &MockKubeClient{}
	cluster := fakeClusterWithRoles("my-db", "default", []interface{}{fakeRole("appuser")})
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(cluster, nil)

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/default/my-db/roles", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.RolesListResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 1, resp.Count)
	assert.Equal(t, "appuser", resp.Roles[0].Name)
	m.AssertExpectations(t)
}

func TestListRoles_EmptyCluster(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(fakeCluster("my-db", "default"), nil)

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/default/my-db/roles", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.RolesListResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Count)
	m.AssertExpectations(t)
}

func TestListRoles_ClusterNotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "missing").Return(nil, notFoundErr())

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/default/missing/roles", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}

// ===== CreateRole =====

func TestCreateRole_Success(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(fakeCluster("my-db", "default"), nil)
	m.On("CreateSecret", mock.Anything, "default", "cnpg-my-db-user-appuser", "appuser", mock.AnythingOfType("string"), mock.Anything).Return(nil)
	m.On("UpdateCluster", mock.Anything, "default", "my-db", mock.Anything).Return(fakeCluster("my-db", "default"), nil)

	body, _ := json.Marshal(models.CreateRoleRequest{
		RoleName: "appuser",
		Login:    true,
		Inherit:  true,
	})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clusters/default/my-db/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp models.RoleResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, "appuser", resp.Role.Name)
	m.AssertExpectations(t)
}

func TestCreateRole_AlreadyExists(t *testing.T) {
	m := &MockKubeClient{}
	cluster := fakeClusterWithRoles("my-db", "default", []interface{}{fakeRole("appuser")})
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(cluster, nil)

	body, _ := json.Marshal(models.CreateRoleRequest{RoleName: "appuser", Login: true})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clusters/default/my-db/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	m.AssertExpectations(t)
}

func TestCreateRole_InvalidName(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(fakeCluster("my-db", "default"), nil)

	body, _ := json.Marshal(models.CreateRoleRequest{RoleName: "UPPER_CASE"})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clusters/default/my-db/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateRole_DryRun(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(fakeCluster("my-db", "default"), nil)

	body, _ := json.Marshal(models.CreateRoleRequest{RoleName: "newuser", Login: true, DryRun: true})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/clusters/default/my-db/roles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
	m.AssertExpectations(t)
}

// ===== UpdateRole =====

func TestUpdateRole_Success(t *testing.T) {
	cluster := fakeClusterWithRoles("my-db", "default", []interface{}{fakeRole("appuser")})
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(cluster, nil)
	m.On("UpdateCluster", mock.Anything, "default", "my-db", mock.Anything).Return(cluster, nil)

	superuser := true
	body, _ := json.Marshal(models.UpdateRoleRequest{Superuser: &superuser})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/clusters/default/my-db/roles/appuser", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.RoleResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	m.AssertExpectations(t)
}

func TestUpdateRole_NotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(fakeCluster("my-db", "default"), nil)

	body, _ := json.Marshal(models.UpdateRoleRequest{})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/clusters/default/my-db/roles/missing", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}

func TestUpdateRole_WithPassword(t *testing.T) {
	cluster := fakeClusterWithRoles("my-db", "default", []interface{}{fakeRole("appuser")})
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(cluster, nil)
	m.On("UpdateSecretPassword", mock.Anything, "default", "cnpg-my-db-user-appuser", "newpass123").Return(nil)
	m.On("UpdateCluster", mock.Anything, "default", "my-db", mock.Anything).Return(cluster, nil)

	newPass := "newpass123"
	body, _ := json.Marshal(models.UpdateRoleRequest{Password: &newPass})

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/clusters/default/my-db/roles/appuser", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	m.AssertExpectations(t)
}

// ===== DeleteRole =====

func TestDeleteRole_Success(t *testing.T) {
	cluster := fakeClusterWithRoles("my-db", "default", []interface{}{fakeRole("appuser")})
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(cluster, nil)
	m.On("UpdateCluster", mock.Anything, "default", "my-db", mock.Anything).Return(cluster, nil)
	m.On("DeleteSecret", mock.Anything, "default", "cnpg-my-db-user-appuser").Return(nil)

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/clusters/default/my-db/roles/appuser", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.MessageResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	m.AssertExpectations(t)
}

func TestDeleteRole_DryRun(t *testing.T) {
	cluster := fakeClusterWithRoles("my-db", "default", []interface{}{fakeRole("appuser")})
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(cluster, nil)
	m.On("SecretExists", mock.Anything, "default", "cnpg-my-db-user-appuser").Return(true)

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/clusters/default/my-db/roles/appuser?dry_run=true", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp models.DryRunResponse
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.DryRun)
	m.AssertExpectations(t)
}

func TestDeleteRole_NotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "my-db").Return(fakeCluster("my-db", "default"), nil)

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/clusters/default/my-db/roles/nonexistent", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}

func TestDeleteRole_ClusterNotFound(t *testing.T) {
	m := &MockKubeClient{}
	m.On("GetCluster", mock.Anything, "default", "no-cluster").
		Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "no-cluster"))

	r := setupRoleRouter(m)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/clusters/default/no-cluster/roles/appuser", nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	m.AssertExpectations(t)
}
