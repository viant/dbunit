module github.com/viant/dbunit

go 1.25.8

require (
	github.com/go-sql-driver/mysql v1.7.0
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.6
	github.com/viant/afs v1.30.1-0.20260914155934-90e95d483861
	github.com/viant/assertly v0.9.1-0.20220620174148-bab013f93a60
	github.com/viant/sqlparser v0.13.1-0.20261005175605-18369aade19d
	github.com/viant/sqlx v0.26.1-0.20261019190555-2b47eeb3f37f
	github.com/viant/structology v0.10.1-0.20260915165514-89d421971772
	github.com/viant/toolbox v0.39.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.21.2
)

require (
	github.com/aerospike/aerospike-client-go v4.5.2+incompatible // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/francoispqt/gojay v1.2.13 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/nxadm/tail v1.4.11 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/viant/parsly v0.3.3 // indirect
	github.com/viant/tagly v0.3.1-0.20260914020630-eadee36c3630 // indirect
	github.com/viant/xreflect v0.7.5-0.20260314170600-13f09f37d46e // indirect
	github.com/viant/xunsafe v0.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/tools v0.44.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	lukechampine.com/uint128 v1.3.0 // indirect
	modernc.org/cc/v3 v3.40.0 // indirect
	modernc.org/ccgo/v3 v3.16.13 // indirect
	modernc.org/libc v1.22.4 // indirect
	modernc.org/mathutil v1.5.0 // indirect
	modernc.org/memory v1.5.0 // indirect
	modernc.org/opt v0.1.3 // indirect
	modernc.org/strutil v1.1.3 // indirect
	modernc.org/token v1.1.0 // indirect
)

replace github.com/viant/sqlx => ../sqlx

replace github.com/viant/structology => ../structology

replace github.com/viant/afs => ../afs

replace github.com/viant/assertly => ../assertly

replace github.com/viant/toolbox => ../toolbox
