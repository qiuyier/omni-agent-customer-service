// Package schema defines the database contract shared by the runtime adapter
// and the separate migration command without making the server depend on Tern.
package schema

const (
	VersionTable = "public.customer_service_schema_version"

	// CurrentVersion is the schema version required by this application build.
	// Increment it whenever a new sequential migration is added.
	CurrentVersion int32 = 3
)
