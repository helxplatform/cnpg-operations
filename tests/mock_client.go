package tests

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/mock"
)

// MockKubeClient is a testify mock implementing k8s.KubeClient.
type MockKubeClient struct {
	mock.Mock
}

func (m *MockKubeClient) GetDefaultNamespace() string {
	return m.Called().String(0)
}

func (m *MockKubeClient) ListClusters(ctx context.Context, namespace string) ([]map[string]interface{}, error) {
	args := m.Called(ctx, namespace)
	v, _ := args.Get(0).([]map[string]interface{})
	return v, args.Error(1)
}

func (m *MockKubeClient) GetCluster(ctx context.Context, namespace, name string) (map[string]interface{}, error) {
	args := m.Called(ctx, namespace, name)
	v, _ := args.Get(0).(map[string]interface{})
	return v, args.Error(1)
}

func (m *MockKubeClient) CreateCluster(ctx context.Context, spec map[string]interface{}) (map[string]interface{}, error) {
	args := m.Called(ctx, spec)
	v, _ := args.Get(0).(map[string]interface{})
	return v, args.Error(1)
}

func (m *MockKubeClient) UpdateCluster(ctx context.Context, namespace, name string, spec map[string]interface{}) (map[string]interface{}, error) {
	args := m.Called(ctx, namespace, name, spec)
	v, _ := args.Get(0).(map[string]interface{})
	return v, args.Error(1)
}

func (m *MockKubeClient) DeleteCluster(ctx context.Context, namespace, name string) error {
	return m.Called(ctx, namespace, name).Error(0)
}

func (m *MockKubeClient) ListSecretsByLabel(ctx context.Context, namespace, labelSelector string) ([]corev1.Secret, error) {
	args := m.Called(ctx, namespace, labelSelector)
	v, _ := args.Get(0).([]corev1.Secret)
	return v, args.Error(1)
}

func (m *MockKubeClient) CreateSecret(ctx context.Context, namespace, name, username, password string, labels map[string]string) error {
	return m.Called(ctx, namespace, name, username, password, labels).Error(0)
}

func (m *MockKubeClient) UpdateSecretPassword(ctx context.Context, namespace, name, password string) error {
	return m.Called(ctx, namespace, name, password).Error(0)
}

func (m *MockKubeClient) DeleteSecret(ctx context.Context, namespace, name string) error {
	return m.Called(ctx, namespace, name).Error(0)
}

func (m *MockKubeClient) SecretExists(ctx context.Context, namespace, name string) bool {
	return m.Called(ctx, namespace, name).Bool(0)
}

func (m *MockKubeClient) DeleteSecretsByLabel(ctx context.Context, namespace, labelSelector string) (int, error) {
	args := m.Called(ctx, namespace, labelSelector)
	return args.Int(0), args.Error(1)
}

func (m *MockKubeClient) ListDatabases(ctx context.Context, namespace, clusterName string) ([]map[string]interface{}, error) {
	args := m.Called(ctx, namespace, clusterName)
	v, _ := args.Get(0).([]map[string]interface{})
	return v, args.Error(1)
}

func (m *MockKubeClient) GetDatabase(ctx context.Context, namespace, name string) (map[string]interface{}, error) {
	args := m.Called(ctx, namespace, name)
	v, _ := args.Get(0).(map[string]interface{})
	return v, args.Error(1)
}

func (m *MockKubeClient) CreateDatabase(ctx context.Context, namespace string, spec map[string]interface{}) error {
	return m.Called(ctx, namespace, spec).Error(0)
}

func (m *MockKubeClient) DeleteDatabase(ctx context.Context, namespace, name string) error {
	return m.Called(ctx, namespace, name).Error(0)
}
