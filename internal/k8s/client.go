package k8s

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	CNPGGroup       = "postgresql.cnpg.io"
	CNPGVersion     = "v1"
	ClusterPlural   = "clusters"
	DatabasePlural  = "databases"
	DefaultNS       = "default"
	passwordCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var (
	clusterGVR = schema.GroupVersionResource{
		Group:    CNPGGroup,
		Version:  CNPGVersion,
		Resource: ClusterPlural,
	}
	databaseGVR = schema.GroupVersionResource{
		Group:    CNPGGroup,
		Version:  CNPGVersion,
		Resource: DatabasePlural,
	}
	rfc1123Re = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// KubeClient defines all Kubernetes operations used by handlers.
// The interface enables dependency injection and easy mocking in tests.
type KubeClient interface {
	GetDefaultNamespace() string

	// Cluster operations
	ListClusters(ctx context.Context, namespace string) ([]map[string]interface{}, error)
	GetCluster(ctx context.Context, namespace, name string) (map[string]interface{}, error)
	CreateCluster(ctx context.Context, spec map[string]interface{}) (map[string]interface{}, error)
	UpdateCluster(ctx context.Context, namespace, name string, spec map[string]interface{}) (map[string]interface{}, error)
	DeleteCluster(ctx context.Context, namespace, name string) error

	// Secret operations
	ListSecretsByLabel(ctx context.Context, namespace, labelSelector string) ([]corev1.Secret, error)
	CreateSecret(ctx context.Context, namespace, name, username, password string, labels map[string]string) error
	UpdateSecretPassword(ctx context.Context, namespace, name, password string) error
	DeleteSecret(ctx context.Context, namespace, name string) error
	SecretExists(ctx context.Context, namespace, name string) bool
	DeleteSecretsByLabel(ctx context.Context, namespace, labelSelector string) (int, error)

	// Database operations
	ListDatabases(ctx context.Context, namespace, clusterName string) ([]map[string]interface{}, error)
	GetDatabase(ctx context.Context, namespace, name string) (map[string]interface{}, error)
	CreateDatabase(ctx context.Context, namespace string, spec map[string]interface{}) error
	DeleteDatabase(ctx context.Context, namespace, name string) error
}

// Client implements KubeClient using the Kubernetes dynamic and core clients.
type Client struct {
	dynamic    dynamic.Interface
	kubernetes kubernetes.Interface
	namespace  string
}

// NewClient creates a new Client, trying in-cluster config then kubeconfig.
func NewClient() (*Client, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build kubernetes config: %w", err)
		}
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Client{
		dynamic:    dynClient,
		kubernetes: kubeClient,
		namespace:  resolveDefaultNamespace(),
	}, nil
}

func resolveDefaultNamespace() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	if ns := os.Getenv("DEFAULT_NAMESPACE"); ns != "" {
		return ns
	}
	return DefaultNS
}

// GetDefaultNamespace returns the default namespace for this client.
func (c *Client) GetDefaultNamespace() string { return c.namespace }

// ===== Cluster Operations =====

// ListClusters lists CNPG clusters, across all namespaces when namespace is "".
func (c *Client) ListClusters(ctx context.Context, namespace string) ([]map[string]interface{}, error) {
	var list *unstructured.UnstructuredList
	var err error
	if namespace == "" {
		list, err = c.dynamic.Resource(clusterGVR).List(ctx, metav1.ListOptions{})
	} else {
		list, err = c.dynamic.Resource(clusterGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		result[i] = item.Object
	}
	return result, nil
}

// GetCluster retrieves a single CNPG cluster by namespace and name.
func (c *Client) GetCluster(ctx context.Context, namespace, name string) (map[string]interface{}, error) {
	obj, err := c.dynamic.Resource(clusterGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return obj.Object, nil
}

// CreateCluster creates a new CNPG cluster from an unstructured spec map.
func (c *Client) CreateCluster(ctx context.Context, spec map[string]interface{}) (map[string]interface{}, error) {
	ns := nestedStr(spec, "metadata", "namespace")
	obj := &unstructured.Unstructured{Object: spec}
	result, err := c.dynamic.Resource(clusterGVR).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return result.Object, nil
}

// UpdateCluster performs a full update (PUT) on an existing cluster.
func (c *Client) UpdateCluster(ctx context.Context, namespace, name string, spec map[string]interface{}) (map[string]interface{}, error) {
	obj := &unstructured.Unstructured{Object: spec}
	result, err := c.dynamic.Resource(clusterGVR).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}
	return result.Object, nil
}

// DeleteCluster deletes a CNPG cluster by namespace and name.
func (c *Client) DeleteCluster(ctx context.Context, namespace, name string) error {
	return c.dynamic.Resource(clusterGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ===== Secret Operations =====

// ListSecretsByLabel returns secrets matching the given label selector.
func (c *Client) ListSecretsByLabel(ctx context.Context, namespace, labelSelector string) ([]corev1.Secret, error) {
	list, err := c.kubernetes.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// CreateSecret creates a kubernetes.io/basic-auth secret with username and password.
func (c *Client) CreateSecret(ctx context.Context, namespace, name, username, password string, labels map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeBasicAuth,
		Data: map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		},
	}
	_, err := c.kubernetes.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

// UpdateSecretPassword updates the password field in an existing secret.
func (c *Client) UpdateSecretPassword(ctx context.Context, namespace, name, password string) error {
	secret, err := c.kubernetes.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	secret.Data["password"] = []byte(password)
	_, err = c.kubernetes.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

// DeleteSecret deletes a secret; returns nil if not found.
func (c *Client) DeleteSecret(ctx context.Context, namespace, name string) error {
	err := c.kubernetes.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}

// SecretExists returns true if the named secret exists.
func (c *Client) SecretExists(ctx context.Context, namespace, name string) bool {
	_, err := c.kubernetes.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	return err == nil
}

// DeleteSecretsByLabel deletes all secrets matching the label selector and returns the count deleted.
func (c *Client) DeleteSecretsByLabel(ctx context.Context, namespace, labelSelector string) (int, error) {
	secrets, err := c.ListSecretsByLabel(ctx, namespace, labelSelector)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, s := range secrets {
		if err := c.DeleteSecret(ctx, namespace, s.Name); err == nil {
			count++
		}
	}
	return count, nil
}

// ===== Database Operations =====

// ListDatabases lists Database CRDs for the given cluster using label selector.
func (c *Client) ListDatabases(ctx context.Context, namespace, clusterName string) ([]map[string]interface{}, error) {
	list, err := c.dynamic.Resource(databaseGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("cnpg.io/cluster=%s", clusterName),
	})
	if err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, len(list.Items))
	for i, item := range list.Items {
		result[i] = item.Object
	}
	return result, nil
}

// GetDatabase retrieves a single Database CRD by namespace and name.
func (c *Client) GetDatabase(ctx context.Context, namespace, name string) (map[string]interface{}, error) {
	obj, err := c.dynamic.Resource(databaseGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return obj.Object, nil
}

// CreateDatabase creates a Database CRD.
func (c *Client) CreateDatabase(ctx context.Context, namespace string, spec map[string]interface{}) error {
	obj := &unstructured.Unstructured{Object: spec}
	_, err := c.dynamic.Resource(databaseGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

// DeleteDatabase deletes a Database CRD by namespace and name.
func (c *Client) DeleteDatabase(ctx context.Context, namespace, name string) error {
	return c.dynamic.Resource(databaseGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ===== Utility Functions =====

// ValidateRFC1123 checks that name is a valid Kubernetes resource name.
func ValidateRFC1123(name, resourceType string) error {
	if name == "" {
		return fmt.Errorf("%s name cannot be empty", resourceType)
	}
	if len(name) > 63 {
		return fmt.Errorf("%s name %q exceeds 63 character limit", resourceType, name)
	}
	if !rfc1123Re.MatchString(name) {
		return fmt.Errorf("%s name %q is invalid: must be lowercase alphanumeric or '-', start and end with alphanumeric", resourceType, name)
	}
	return nil
}

// GeneratePassword creates a cryptographically random alphanumeric password.
func GeneratePassword(length int) (string, error) {
	buf := make([]byte, length)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordCharset))))
		if err != nil {
			return "", err
		}
		buf[i] = passwordCharset[n.Int64()]
	}
	return string(buf), nil
}

// nestedStr is a convenience wrapper for unstructured.NestedString.
func nestedStr(obj map[string]interface{}, fields ...string) string {
	v, _, _ := unstructured.NestedString(obj, fields...)
	return v
}
