// Package main is the entry point for the CNPG REST server.
//
// @title           CNPG REST API
// @version         1.0
// @description     REST API for managing CloudNativePG PostgreSQL clusters on Kubernetes.
// @description     Provides full cluster lifecycle management, role/user management, and database management.
//
// @contact.name    CNPG Operations
//
// @license.name    Apache 2.0
// @license.url     https://www.apache.org/licenses/LICENSE-2.0
//
// @host            localhost:8080
// @BasePath        /api/v1
// @schemes         http https
package main

import (
	"log"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/cnpg-operations/cnpg-rest-server/docs"
	"github.com/cnpg-operations/cnpg-rest-server/internal/handlers"
	"github.com/cnpg-operations/cnpg-rest-server/internal/k8s"
)

func main() {
	client, err := k8s.NewClient()
	if err != nil {
		log.Fatalf("failed to initialize Kubernetes client: %v", err)
	}

	clusterH := handlers.NewClusterHandler(client)
	roleH := handlers.NewRoleHandler(client)
	dbH := handlers.NewDatabaseHandler(client)

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
	}

	r := gin.New()
	if logLevel == "debug" {
		r.Use(gin.Logger())
	} else {
		r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
			SkipPaths: []string{"/health", "/ready"},
		}))
	}
	r.Use(gin.Recovery())

	// Health / readiness
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.GET("/ready", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ready"})
	})

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	v1 := r.Group("/api/v1")
	{
		// Cluster management
		v1.GET("/clusters", clusterH.ListClusters)
		v1.POST("/clusters", clusterH.CreateCluster)
		v1.GET("/clusters/:namespace/:name", clusterH.GetCluster)
		v1.PATCH("/clusters/:namespace/:name/scale", clusterH.ScaleCluster)
		v1.DELETE("/clusters/:namespace/:name", clusterH.DeleteCluster)

		// Role management
		v1.GET("/clusters/:namespace/:name/roles", roleH.ListRoles)
		v1.POST("/clusters/:namespace/:name/roles", roleH.CreateRole)
		v1.PUT("/clusters/:namespace/:name/roles/:role", roleH.UpdateRole)
		v1.DELETE("/clusters/:namespace/:name/roles/:role", roleH.DeleteRole)

		// Database management
		v1.GET("/clusters/:namespace/:name/databases", dbH.ListDatabases)
		v1.POST("/clusters/:namespace/:name/databases", dbH.CreateDatabase)
		v1.DELETE("/clusters/:namespace/:name/databases/:database", dbH.DeleteDatabase)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("CNPG REST server listening on :%s  swagger: http://localhost:%s/swagger/index.html", port, port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
