package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func DramaExternalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		secret := common.GetEnvOrDefaultString("NEW_API_EXTERNAL_SECRET", "")
		if secret == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "NEW_API_EXTERNAL_SECRET is not configured",
			})
			c.Abort()
			return
		}

		token := c.GetHeader("Authorization")
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		if token != secret {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "invalid external secret",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
