package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/cnpg-operations/cnpg-rest-server/internal/k8s"
	"github.com/cnpg-operations/cnpg-rest-server/internal/models"
)

// resolveNamespace returns the namespace query parameter, falling back to the
// client's default namespace (i.e. the namespace the server is running in).
func resolveNamespace(c *gin.Context, client k8s.KubeClient) string {
	if ns := c.Query("namespace"); ns != "" {
		return ns
	}
	return client.GetDefaultNamespace()
}

// respondError writes a JSON error response.
func respondError(c *gin.Context, status int, err error) {
	c.JSON(status, models.ErrorResponse{
		Success: false,
		Error:   err.Error(),
		Code:    status,
	})
}

// k8sStatus maps Kubernetes API errors to HTTP status codes.
func k8sStatus(err error) int {
	switch {
	case k8serrors.IsNotFound(err):
		return http.StatusNotFound
	case k8serrors.IsAlreadyExists(err):
		return http.StatusConflict
	case k8serrors.IsForbidden(err):
		return http.StatusForbidden
	case k8serrors.IsUnauthorized(err):
		return http.StatusUnauthorized
	case k8serrors.IsInvalid(err):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// ensureOwnerRole checks whether the named role exists in the cluster's
// spec.managed.roles.  If it does not, it creates a password secret and
// appends the role (login-enabled) to the cluster spec.
// Returns (secretName, created, error).
func ensureOwnerRole(ctx context.Context, client k8s.KubeClient, namespace, clusterName, roleName string) (string, bool, error) {
	obj, err := client.GetCluster(ctx, namespace, clusterName)
	if err != nil {
		return "", false, err
	}

	rawRoles := nestedSlice(obj, "spec", "managed", "roles")
	for _, r := range rawRoles {
		if roleMap, ok := r.(map[string]interface{}); ok {
			if nestedStr(roleMap, "name") == roleName {
				secretName := nestedStr(roleMap, "passwordSecret", "name")
				return secretName, false, nil
			}
		}
	}

	// Role does not exist — create it
	secretName := roleSecretName(clusterName, roleName)
	if err := k8s.ValidateRFC1123(secretName, "Role secret"); err != nil {
		return "", false, err
	}

	password, err := k8s.GeneratePassword(16)
	if err != nil {
		return "", false, fmt.Errorf("failed to generate password: %w", err)
	}

	if err := client.CreateSecret(ctx, namespace, secretName, roleName, password, map[string]string{
		"app.kubernetes.io/name": "cnpg",
		"cnpg.io/cluster":        clusterName,
		"cnpg.io/role":           roleName,
	}); err != nil {
		return "", false, fmt.Errorf("failed to create password secret: %w", err)
	}

	newRole := map[string]interface{}{
		"name":        roleName,
		"ensure":      "present",
		"login":       true,
		"superuser":   false,
		"inherit":     true,
		"createdb":    false,
		"createrole":  false,
		"replication": false,
		"passwordSecret": map[string]interface{}{
			"name": secretName,
		},
	}

	obj = ensureManagedRoles(obj)
	obj["spec"].(map[string]interface{})["managed"].(map[string]interface{})["roles"] = append(rawRoles, newRole)

	if _, err := client.UpdateCluster(ctx, namespace, clusterName, obj); err != nil {
		// Best-effort cleanup
		_ = client.DeleteSecret(ctx, namespace, secretName)
		return "", false, err
	}

	return secretName, true, nil
}

// nestedStr is a nil-safe helper that extracts a string from a nested map.
func nestedStr(obj map[string]interface{}, keys ...string) string {
	cur := obj
	for i, k := range keys {
		if i == len(keys)-1 {
			if v, ok := cur[k].(string); ok {
				return v
			}
			return ""
		}
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return ""
		}
		cur = next
	}
	return ""
}

// nestedInt extracts an int from a nested map; JSON numbers are float64.
func nestedInt(obj map[string]interface{}, keys ...string) int {
	cur := obj
	for i, k := range keys {
		if i == len(keys)-1 {
			switch v := cur[k].(type) {
			case float64:
				return int(v)
			case int64:
				return int(v)
			case int:
				return v
			}
			return 0
		}
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return 0
		}
		cur = next
	}
	return 0
}

// nestedBool extracts a bool from a nested map.
func nestedBool(obj map[string]interface{}, keys ...string) bool {
	cur := obj
	for i, k := range keys {
		if i == len(keys)-1 {
			if v, ok := cur[k].(bool); ok {
				return v
			}
			return false
		}
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// nestedSlice extracts a []interface{} from a nested map.
func nestedSlice(obj map[string]interface{}, keys ...string) []interface{} {
	cur := obj
	for i, k := range keys {
		if i == len(keys)-1 {
			if v, ok := cur[k].([]interface{}); ok {
				return v
			}
			return nil
		}
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return nil
		}
		cur = next
	}
	return nil
}
