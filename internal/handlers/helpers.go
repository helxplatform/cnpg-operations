package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/cnpg-operations/cnpg-rest-server/internal/models"
)

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
