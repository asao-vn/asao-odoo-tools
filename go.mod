module github.com/asao-platform/atoc/packages/cli

go 1.26.6

require (
	github.com/BurntSushi/toml v1.5.0
	github.com/asao-platform/atoc/packages/odoo v0.0.0
	github.com/asao-platform/atoc/packages/platform v0.0.0-00010101000000-000000000000
	github.com/zalando/go-keyring v0.2.8
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

require golang.org/x/sys v0.48.0 // indirect

replace (
	github.com/asao-platform/atoc/packages/odoo => ./third_party/packages/odoo
	github.com/asao-platform/atoc/packages/platform => ./third_party/packages/platform
)
