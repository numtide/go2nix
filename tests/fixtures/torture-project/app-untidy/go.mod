module github.com/numtide/go2nix/torture/app-untidy

go 1.25

require (
	github.com/numtide/go2nix/torture/internal/common v0.0.0
	github.com/numtide/go2nix/torture/internal/aws v0.0.0
	github.com/numtide/go2nix/torture/internal/gcp v0.0.0
	github.com/numtide/go2nix/torture/internal/azure v0.0.0
	github.com/numtide/go2nix/torture/internal/k8s v0.0.0
	github.com/numtide/go2nix/torture/internal/web v0.0.0
	github.com/numtide/go2nix/torture/internal/db v0.0.0
	github.com/numtide/go2nix/torture/internal/observability v0.0.0
	github.com/numtide/go2nix/torture/internal/crypto v0.0.0
	github.com/numtide/go2nix/torture/internal/data v0.0.0
	github.com/numtide/go2nix/torture/internal/networking v0.0.0
	github.com/numtide/go2nix/torture/internal/testing v0.0.0
	github.com/numtide/go2nix/torture/internal/conflict-a v0.0.0
	github.com/numtide/go2nix/torture/internal/conflict-b v0.0.0
)

replace (
	github.com/numtide/go2nix/torture/internal/common => ../internal/common
	github.com/numtide/go2nix/torture/internal/aws => ../internal/aws
	github.com/numtide/go2nix/torture/internal/gcp => ../internal/gcp
	github.com/numtide/go2nix/torture/internal/azure => ../internal/azure
	github.com/numtide/go2nix/torture/internal/k8s => ../internal/k8s
	github.com/numtide/go2nix/torture/internal/web => ../internal/web
	github.com/numtide/go2nix/torture/internal/db => ../internal/db
	github.com/numtide/go2nix/torture/internal/observability => ../internal/observability
	github.com/numtide/go2nix/torture/internal/crypto => ../internal/crypto
	github.com/numtide/go2nix/torture/internal/data => ../internal/data
	github.com/numtide/go2nix/torture/internal/networking => ../internal/networking
	github.com/numtide/go2nix/torture/internal/testing => ../internal/testing
	github.com/numtide/go2nix/torture/internal/conflict-a => ../internal/conflict-a
	github.com/numtide/go2nix/torture/internal/conflict-b => ../internal/conflict-b
	github.com/go-chi/chi/v5 => github.com/go-chi/chi/v5 v5.2.0
	github.com/go-sql-driver/mysql => github.com/go-sql-driver/mysql v1.9.0
	github.com/gin-gonic/gin => github.com/gin-gonic/gin v1.9.1
	github.com/redis/go-redis/v9 => github.com/redis/go-redis/v9 v9.7.0
	github.com/jackc/pgx/v5 => github.com/jackc/pgx/v5 v5.7.2
	go.uber.org/zap => go.uber.org/zap v1.27.0
)
