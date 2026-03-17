package models

// ===== Request Types =====

// CreateClusterRequest is the request body for creating a new PostgreSQL cluster.
type CreateClusterRequest struct {
	Name            string `json:"name" binding:"required" example:"my-db"`
	Instances       int    `json:"instances" example:"3"`
	StorageSize     string `json:"storage_size" example:"10Gi"`
	PostgresVersion string `json:"postgres_version" example:"16"`
	StorageClass    string `json:"storage_class,omitempty" example:"fast-ssd"`
	Namespace       string `json:"namespace,omitempty" example:"default"`
	Wait            bool   `json:"wait,omitempty" example:"false"`
	Timeout         *int   `json:"timeout,omitempty" example:"180"`
	DryRun          bool   `json:"dry_run,omitempty" example:"false"`
}

// ScaleClusterRequest is the request body for scaling a cluster.
type ScaleClusterRequest struct {
	Instances int  `json:"instances" binding:"required,min=1,max=10" example:"5"`
	DryRun    bool `json:"dry_run,omitempty" example:"false"`
}

// CreateRoleRequest is the request body for creating a PostgreSQL role.
type CreateRoleRequest struct {
	RoleName    string `json:"role_name" binding:"required" example:"myuser"`
	Login       bool   `json:"login" example:"true"`
	Superuser   bool   `json:"superuser,omitempty" example:"false"`
	Inherit     bool   `json:"inherit" example:"true"`
	CreateDB    bool   `json:"createdb,omitempty" example:"false"`
	CreateRole  bool   `json:"createrole,omitempty" example:"false"`
	Replication bool   `json:"replication,omitempty" example:"false"`
	DryRun      bool   `json:"dry_run,omitempty" example:"false"`
}

// UpdateRoleRequest is the request body for updating a PostgreSQL role.
type UpdateRoleRequest struct {
	Login       *bool   `json:"login,omitempty" example:"true"`
	Superuser   *bool   `json:"superuser,omitempty" example:"false"`
	Inherit     *bool   `json:"inherit,omitempty" example:"true"`
	CreateDB    *bool   `json:"createdb,omitempty" example:"false"`
	CreateRole  *bool   `json:"createrole,omitempty" example:"false"`
	Replication *bool   `json:"replication,omitempty" example:"false"`
	Password    *string `json:"password,omitempty" example:"newpassword123"`
	DryRun      bool    `json:"dry_run,omitempty" example:"false"`
}

// CreateDatabaseRequest is the request body for creating a PostgreSQL database.
type CreateDatabaseRequest struct {
	DatabaseName  string `json:"database_name" binding:"required" example:"mydb"`
	Owner         string `json:"owner" binding:"required" example:"myuser"`
	ReclaimPolicy string `json:"reclaim_policy,omitempty" example:"retain"`
	DryRun        bool   `json:"dry_run,omitempty" example:"false"`
}

// ===== Response Types =====

// ErrorResponse is returned on any error.
type ErrorResponse struct {
	Success bool   `json:"success" example:"false"`
	Error   string `json:"error" example:"cluster not found"`
	Code    int    `json:"code,omitempty" example:"404"`
}

// MessageResponse is a simple success message response.
type MessageResponse struct {
	Success bool   `json:"success" example:"true"`
	Message string `json:"message" example:"operation completed successfully"`
}

// DryRunResponse is returned when dry_run=true.
type DryRunResponse struct {
	Success bool   `json:"success" example:"true"`
	DryRun  bool   `json:"dry_run" example:"true"`
	Message string `json:"message" example:"dry run completed"`
	Preview string `json:"preview,omitempty"`
}

// ClusterInfo holds information about a CloudNativePG cluster.
type ClusterInfo struct {
	Name            string        `json:"name" example:"my-db"`
	Namespace       string        `json:"namespace" example:"default"`
	Instances       int           `json:"instances" example:"3"`
	ReadyInstances  int           `json:"ready_instances" example:"3"`
	Phase           string        `json:"phase" example:"Cluster in healthy state"`
	CurrentPrimary  string        `json:"current_primary" example:"my-db-1"`
	PostgresVersion string        `json:"postgres_version,omitempty" example:"ghcr.io/cloudnative-pg/postgresql:16"`
	StorageSize     string        `json:"storage_size,omitempty" example:"10Gi"`
	StorageClass    string        `json:"storage_class,omitempty" example:"standard"`
	Conditions      []interface{} `json:"conditions,omitempty"`
	PGParameters    interface{}   `json:"postgresql_parameters,omitempty"`
}

// ClustersListResponse is returned by the list clusters endpoint.
type ClustersListResponse struct {
	Success  bool          `json:"success" example:"true"`
	Clusters []ClusterInfo `json:"clusters"`
	Count    int           `json:"count" example:"2"`
	Scope    string        `json:"scope" example:"all namespaces"`
}

// ClusterResponse wraps a single ClusterInfo.
type ClusterResponse struct {
	Success bool        `json:"success" example:"true"`
	Cluster ClusterInfo `json:"cluster"`
}

// ClusterCreateResponse is returned after creating a cluster.
type ClusterCreateResponse struct {
	Success   bool   `json:"success" example:"true"`
	Message   string `json:"message" example:"cluster created"`
	Name      string `json:"name" example:"my-db"`
	Namespace string `json:"namespace" example:"default"`
}

// RoleInfo holds information about a managed PostgreSQL role.
type RoleInfo struct {
	Name           string   `json:"name" example:"myuser"`
	Ensure         string   `json:"ensure" example:"present"`
	Login          bool     `json:"login" example:"true"`
	Superuser      bool     `json:"superuser" example:"false"`
	Inherit        bool     `json:"inherit" example:"true"`
	CreateDB       bool     `json:"createdb" example:"false"`
	CreateRole     bool     `json:"createrole" example:"false"`
	Replication    bool     `json:"replication" example:"false"`
	PasswordSecret string   `json:"password_secret" example:"cnpg-my-db-user-myuser"`
	InRoles        []string `json:"in_roles,omitempty"`
}

// RolesListResponse is returned by the list roles endpoint.
type RolesListResponse struct {
	Success bool       `json:"success" example:"true"`
	Cluster string     `json:"cluster" example:"default/my-db"`
	Roles   []RoleInfo `json:"roles"`
	Count   int        `json:"count" example:"1"`
}

// RoleResponse wraps a single RoleInfo.
type RoleResponse struct {
	Success bool     `json:"success" example:"true"`
	Role    RoleInfo `json:"role"`
	Message string   `json:"message,omitempty"`
}

// DatabaseInfo holds information about a managed PostgreSQL database.
type DatabaseInfo struct {
	CRDName       string `json:"crd_name" example:"my-db-mydb"`
	DatabaseName  string `json:"database_name" example:"mydb"`
	Owner         string `json:"owner" example:"myuser"`
	Ensure        string `json:"ensure" example:"present"`
	ReclaimPolicy string `json:"reclaim_policy" example:"retain"`
}

// DatabasesListResponse is returned by the list databases endpoint.
type DatabasesListResponse struct {
	Success   bool           `json:"success" example:"true"`
	Cluster   string         `json:"cluster" example:"default/my-db"`
	Databases []DatabaseInfo `json:"databases"`
	Count     int            `json:"count" example:"1"`
}

// DatabaseResponse wraps a single DatabaseInfo.
type DatabaseResponse struct {
	Success  bool         `json:"success" example:"true"`
	Database DatabaseInfo `json:"database"`
	Message  string       `json:"message,omitempty"`
}
