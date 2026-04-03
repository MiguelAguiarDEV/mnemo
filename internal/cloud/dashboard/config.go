package dashboard

// DashboardConfig holds dashboard-specific configuration.
type DashboardConfig struct {
	// AdminEmail is the email of the admin user. When set, the Admin tab is
	// visible to the authenticated user whose email matches this value.
	// Populated from the MNEMO_CLOUD_ADMIN environment variable.
	AdminEmail string
}
