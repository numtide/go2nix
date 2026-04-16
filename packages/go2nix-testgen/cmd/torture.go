// Package cmd implements the torture generator subcommands.
//
// The torture subcommand creates a torture-test multi-module Go project with
// 12 library modules and 1 app module connected via replace directives. Each
// library pulls in a wide variety of heavy dependencies. The generated code
// compiles but does nothing useful at runtime.
package cmd

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// DepCategory groups related imports together.
type DepCategory struct {
	Name    string
	Imports []ImportSpec
}

// ImportSpec describes a single Go import and a safe one-liner usage.
type ImportSpec struct {
	Path  string // full Go import path
	Alias string // import alias (empty = use default package name)
	Usage string // one-liner Go expression that compiles and references the import
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const modulePath = "github.com/numtide/go2nix/torture"

// ModuleDef describes a library module in the generated project.
type ModuleDef struct {
	Name       string
	PkgName    string
	Categories []string
	LocalDeps  []string
	Replaces   map[string]string // pkg -> version, forces exact version via replace
}

var multiModules = []ModuleDef{
	{Name: "common", PkgName: "common", Categories: []string{"config-env", "serialization", "cli"}, LocalDeps: nil},
	{Name: "aws", PkgName: "aws", Categories: []string{"cloud-aws", "cloud-aws-extra"}, LocalDeps: []string{"common"}},
	{Name: "gcp", PkgName: "gcp", Categories: []string{"cloud-gcp"}, LocalDeps: []string{"common"}},
	{Name: "azure", PkgName: "azure", Categories: []string{"cloud-azure"}, LocalDeps: []string{"common"}},
	{Name: "k8s", PkgName: "k8s", Categories: []string{"kubernetes", "k8s-extra"}, LocalDeps: []string{"common"}},
	{Name: "web", PkgName: "web", Categories: []string{"web-frameworks", "template-build"}, LocalDeps: []string{"common", "db"}},
	{Name: "db", PkgName: "db", Categories: []string{"databases", "orm-data"}, LocalDeps: []string{"common"}},
	{Name: "observability", PkgName: "observability", Categories: []string{"observability", "more-observability"}, LocalDeps: []string{"common"}},
	{Name: "crypto", PkgName: "cryptolib", Categories: []string{"crypto", "auth-security"}, LocalDeps: []string{"common"}},
	{Name: "data", PkgName: "data", Categories: []string{"data-processing"}, LocalDeps: []string{"common", "db"}},
	{Name: "networking", PkgName: "networking", Categories: []string{"networking-infra"}, LocalDeps: []string{"common"}},
	{Name: "testing", PkgName: "testinglib", Categories: []string{"testing-tools", "ai-ml"}, LocalDeps: []string{"common"}},
	{Name: "conflict-a", PkgName: "conflicta", Categories: []string{"web-frameworks", "databases"}, LocalDeps: []string{"common"}, Replaces: map[string]string{
		"github.com/gin-gonic/gin":       "v1.9.1",
		"github.com/redis/go-redis/v9":   "v9.5.1",
		"github.com/jackc/pgx/v5":        "v5.6.0",
		"go.uber.org/zap":                "v1.26.0",
		"github.com/go-chi/chi/v5":       "v5.1.0",
		"github.com/go-sql-driver/mysql": "v1.8.1",
	}},
	{Name: "conflict-b", PkgName: "conflictb", Categories: []string{"web-frameworks", "databases"}, LocalDeps: []string{"common"}, Replaces: map[string]string{
		"github.com/gin-gonic/gin":       "v1.10.0",
		"github.com/redis/go-redis/v9":   "v9.7.0",
		"github.com/jackc/pgx/v5":        "v5.7.2",
		"go.uber.org/zap":                "v1.27.0",
		"github.com/go-chi/chi/v5":       "v5.2.0",
		"github.com/go-sql-driver/mysql": "v1.9.0",
	}},
}

// ---------------------------------------------------------------------------
// Dependency catalog (~170 entries across categories)
// ---------------------------------------------------------------------------

var categories = []DepCategory{
	// 1. cloud-aws (~15)
	{
		Name: "cloud-aws",
		Imports: []ImportSpec{
			{Path: "github.com/aws/aws-sdk-go-v2/aws", Usage: `_ = aws.String("x")`},
			{Path: "github.com/aws/aws-sdk-go-v2/config", Alias: "awsconfig", Usage: `_ = awsconfig.WithRegion("us-east-1")`},
			{Path: "github.com/aws/aws-sdk-go-v2/service/s3", Usage: "var _ *s3.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/sts", Usage: "var _ *sts.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/eks", Usage: "var _ *eks.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/dynamodb", Usage: "var _ *dynamodb.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/sqs", Usage: "var _ *sqs.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/lambda", Usage: "var _ *lambda.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/ec2", Usage: "var _ *ec2.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/iam", Usage: "var _ *iam.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/cloudwatch", Usage: "var _ *cloudwatch.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/sns", Usage: "var _ *sns.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/secretsmanager", Usage: "var _ *secretsmanager.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/kms", Usage: "var _ *kms.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/route53", Usage: "var _ *route53.Client"},
		},
	},

	// 2. kubernetes (~10)
	{
		Name: "kubernetes",
		Imports: []ImportSpec{
			{Path: "k8s.io/client-go/kubernetes", Usage: "var _ kubernetes.Interface"},
			{Path: "k8s.io/api/core/v1", Alias: "corev1", Usage: "_ = corev1.Pod{}"},
			{Path: "k8s.io/api/apps/v1", Alias: "appsv1", Usage: "_ = appsv1.Deployment{}"},
			{Path: "k8s.io/apimachinery/pkg/apis/meta/v1", Alias: "metav1", Usage: "_ = metav1.ObjectMeta{}"},
			{Path: "k8s.io/apimachinery/pkg/runtime", Usage: "var _ runtime.Object"},
			{Path: "k8s.io/apimachinery/pkg/types", Alias: "k8stypes", Usage: `_ = k8stypes.NamespacedName{}`},
			{Path: "k8s.io/client-go/rest", Usage: "_ = rest.Config{}"},
			{Path: "k8s.io/client-go/tools/clientcmd", Usage: "_ = clientcmd.NewDefaultClientConfigLoadingRules"},
			{Path: "sigs.k8s.io/controller-runtime", Alias: "ctrl", Usage: "_ = ctrl.Log"},
			{Path: "sigs.k8s.io/controller-runtime/pkg/client", Alias: "ctrlclient", Usage: "var _ ctrlclient.Client"},
			{Path: "k8s.io/client-go/tools/cache", Alias: "k8scache", Usage: "var _ k8scache.Store"},
			{Path: "k8s.io/apimachinery/pkg/labels", Usage: "_ = labels.Everything()"},
		},
	},

	// 3. grpc-protobuf (~8)
	{
		Name: "grpc-protobuf",
		Imports: []ImportSpec{
			{Path: "google.golang.org/grpc", Usage: "var _ *grpc.Server"},
			{Path: "google.golang.org/grpc/credentials", Usage: "var _ credentials.TransportCredentials"},
			{Path: "google.golang.org/grpc/metadata", Usage: "_ = metadata.New(nil)"},
			{Path: "google.golang.org/grpc/codes", Usage: "_ = codes.OK"},
			{Path: "google.golang.org/grpc/status", Usage: `_ = status.Error(0, "")`},
			{Path: "google.golang.org/protobuf/proto", Usage: "var _ proto.Message"},
			{Path: "google.golang.org/protobuf/types/known/timestamppb", Usage: "_ = timestamppb.Now()"},
			{Path: "github.com/grpc-ecosystem/grpc-gateway/v2/runtime", Alias: "gwruntime", Usage: "_ = gwruntime.NewServeMux()"},
			{Path: "google.golang.org/protobuf/types/known/emptypb", Usage: "_ = emptypb.Empty{}"},
			{Path: "google.golang.org/protobuf/types/known/wrapperspb", Usage: `_ = wrapperspb.String("")`},
			{Path: "google.golang.org/protobuf/types/known/structpb", Usage: "_ = structpb.NullValue_NULL_VALUE"},
			{Path: "google.golang.org/grpc/keepalive", Usage: "_ = keepalive.ClientParameters{}"},
		},
	},

	// 4. web-frameworks (~15)
	{
		Name: "web-frameworks",
		Imports: []ImportSpec{
			{Path: "github.com/gin-gonic/gin", Usage: "var _ *gin.Engine"},
			{Path: "github.com/labstack/echo/v4", Alias: "echo", Usage: "var _ *echo.Echo"},
			{Path: "github.com/gofiber/fiber/v2", Alias: "fiber", Usage: "var _ *fiber.App"},
			{Path: "net/http", Usage: "var _ http.Handler"},
			{Path: "github.com/gorilla/mux", Usage: "_ = mux.NewRouter()"},
			{Path: "github.com/gorilla/websocket", Usage: "_ = websocket.Upgrader{}"},
			{Path: "github.com/go-chi/chi/v5", Alias: "chi", Usage: "_ = chi.NewRouter()"},
			{Path: "github.com/julienschmidt/httprouter", Usage: "_ = httprouter.New()"},
			{Path: "github.com/rs/cors", Usage: "_ = cors.Options{}"},
			{Path: "github.com/go-playground/validator/v10", Alias: "validator", Usage: "_ = validator.New()"},
			{Path: "github.com/gorilla/sessions", Usage: "var _ sessions.Store"},
			{Path: "github.com/gorilla/csrf", Usage: "var _ = csrf.Protect"},
			{Path: "github.com/rs/zerolog", Usage: "_ = zerolog.Logger{}"},
			{Path: "github.com/go-resty/resty/v2", Alias: "resty", Usage: "_ = resty.New()"},
			{Path: "github.com/valyala/fasthttp", Usage: "var _ *fasthttp.Server"},
		},
	},

	// 5. databases (~15)
	{
		Name: "databases",
		Imports: []ImportSpec{
			{Path: "github.com/jackc/pgx/v5", Alias: "pgx", Usage: "var _ *pgx.Conn"},
			{Path: "github.com/redis/go-redis/v9", Alias: "redis", Usage: "_ = redis.Options{}"},
			{Path: "go.mongodb.org/mongo-driver/mongo", Usage: "var _ *mongo.Client"},
			{Path: "go.mongodb.org/mongo-driver/bson", Usage: `_ = bson.D{}`},
			{Path: "github.com/go-sql-driver/mysql", Alias: "mysql", Usage: `_ = mysql.Config{}`},
			{Path: "database/sql", Usage: "var _ *sql.DB"},
			{Path: "github.com/jmoiron/sqlx", Usage: "var _ *sqlx.DB"},
			{Path: "go.etcd.io/etcd/client/v3", Alias: "clientv3", Usage: "_ = clientv3.Config{}"},
			{Path: "github.com/dgraph-io/badger/v4", Alias: "badger", Usage: `_ = badger.DefaultOptions("")`},
			{Path: "github.com/cockroachdb/pebble", Usage: "var _ *pebble.DB"},
			{Path: "github.com/elastic/go-elasticsearch/v8", Alias: "elasticsearch", Usage: "_ = elasticsearch.Config{}"},
			{Path: "github.com/mattn/go-sqlite3", Alias: "sqlite3", Usage: `_ = sqlite3.SQLiteDriver{}`},
			{Path: "github.com/lib/pq", Alias: "pq", Usage: `_ = pq.ErrSSLNotSupported`},
			{Path: "github.com/uptrace/bun", Usage: "var _ *bun.DB"},
			{Path: "github.com/pressly/goose/v3", Alias: "goose", Usage: "_ = goose.Up"},
		},
	},

	// 6. observability (~15)
	{
		Name: "observability",
		Imports: []ImportSpec{
			{Path: "github.com/prometheus/client_golang/prometheus", Usage: "var _ prometheus.Collector"},
			{Path: "github.com/prometheus/client_golang/prometheus/promauto", Usage: "_ = promauto.With(nil)"},
			{Path: "go.opentelemetry.io/otel", Usage: "_ = otel.GetTracerProvider()"},
			{Path: "go.opentelemetry.io/otel/trace", Alias: "oteltrace", Usage: "var _ oteltrace.Tracer"},
			{Path: "go.opentelemetry.io/otel/metric", Alias: "otelmetric", Usage: "var _ otelmetric.Meter"},
			{Path: "go.opentelemetry.io/otel/attribute", Usage: `_ = attribute.String("k", "v")`},
			{Path: "go.opentelemetry.io/otel/exporters/otlp/otlptrace", Usage: "var _ otlptrace.Exporter"},
			{Path: "go.opentelemetry.io/otel/sdk/trace", Alias: "sdktrace", Usage: "var _ *sdktrace.TracerProvider"},
			{Path: "go.uber.org/zap", Usage: "_ = zap.NewNop()"},
			{Path: "github.com/sirupsen/logrus", Usage: "_ = logrus.Fields{}"},
			{Path: "log/slog", Usage: "_ = slog.Default()"},
			{Path: "go.uber.org/zap/zapcore", Usage: "_ = zapcore.DebugLevel"},
			{Path: "github.com/grafana/pyroscope-go", Alias: "pyroscope", Usage: "_ = pyroscope.Config{}"},
			{Path: "github.com/DataDog/datadog-go/v5/statsd", Alias: "statsd", Usage: "_ = statsd.Option(nil)"},
			{Path: "github.com/prometheus/common/model", Usage: `_ = model.LabelName("")`},
		},
	},

	// 7. crypto (~10)
	{
		Name: "crypto",
		Imports: []ImportSpec{
			{Path: "golang.org/x/crypto/ssh", Usage: "var _ ssh.Signer"},
			{Path: "golang.org/x/crypto/bcrypt", Usage: "_ = bcrypt.MinCost"},
			{Path: "github.com/golang-jwt/jwt/v5", Alias: "jwt", Usage: "_ = jwt.SigningMethodHS256"},
			{Path: "crypto/tls", Usage: "_ = tls.Config{}"},
			{Path: "crypto/ed25519", Usage: "var _ ed25519.PublicKey"},
			{Path: "crypto/sha256", Usage: "_ = sha256.New()"},
			{Path: "crypto/rand", Usage: "var _ = rand.Reader"},
			{Path: "github.com/cloudflare/circl/sign/ed448", Usage: "var _ ed448.PublicKey"},
			{Path: "golang.org/x/crypto/argon2", Usage: "_ = argon2.IDKey"},
			{Path: "golang.org/x/crypto/nacl/box", Alias: "naclbox", Usage: "_ = naclbox.Overhead"},
			{Path: "golang.org/x/crypto/chacha20poly1305", Usage: "_ = chacha20poly1305.KeySize"},
			{Path: "crypto/hmac", Usage: "var _ = hmac.New"},
			{Path: "crypto/aes", Usage: "_ = aes.BlockSize"},
		},
	},

	// 8. serialization (~10)
	{
		Name: "serialization",
		Imports: []ImportSpec{
			{Path: "encoding/json", Usage: "var _ *json.Decoder"},
			{Path: "github.com/pelletier/go-toml/v2", Alias: "toml", Usage: "var _ *toml.Decoder"},
			{Path: "gopkg.in/yaml.v3", Alias: "yaml", Usage: "var _ *yaml.Decoder"},
			{Path: "github.com/vmihailenco/msgpack/v5", Alias: "msgpack", Usage: "var _ *msgpack.Decoder"},
			{Path: "github.com/fxamacker/cbor/v2", Alias: "cbor", Usage: "_ = cbor.DecOptions{}"},
			{Path: "github.com/BurntSushi/toml", Alias: "btoml", Usage: "var _ *btoml.Decoder"},
			{Path: "github.com/goccy/go-json", Alias: "gojson", Usage: "var _ *gojson.Decoder"},
			{Path: "encoding/xml", Usage: "var _ *xml.Decoder"},
			{Path: "encoding/csv", Usage: "var _ *csv.Reader"},
			{Path: "github.com/klauspost/compress/zstd", Usage: "var _ *zstd.Decoder"},
			{Path: "github.com/mitchellh/hashstructure/v2", Alias: "hashstructure", Usage: "_ = hashstructure.FormatV2"},
			{Path: "github.com/shamaton/msgpack/v2", Alias: "msgpack2", Usage: "var _ = msgpack2.Marshal"},
		},
	},

	// 9. image (~8)
	{
		Name: "image",
		Imports: []ImportSpec{
			{Path: "github.com/disintegration/imaging", Usage: "_ = imaging.Lanczos"},
			{Path: "golang.org/x/image/draw", Alias: "xdraw", Usage: "_ = xdraw.NearestNeighbor"},
			{Path: "image", Usage: "_ = image.Rect(0, 0, 1, 1)"},
			{Path: "image/png", Usage: "var _ png.Encoder"},
			{Path: "image/jpeg", Usage: "var _ = jpeg.Encode"},
			{Path: "image/color", Alias: "imgcolor", Usage: "_ = imgcolor.RGBA{}"},
			{Path: "github.com/fogleman/gg", Usage: "_ = gg.NewContext(1, 1)"},
			{Path: "github.com/srwiley/oksvg", Usage: "var _ *oksvg.SvgIcon"},
			{Path: "github.com/srwiley/rasterx", Usage: "var _ *rasterx.Dasher"},
			{Path: "golang.org/x/image/font", Alias: "xfont", Usage: "var _ xfont.Face"},
		},
	},

	// 10. cli (~10)
	{
		Name: "cli",
		Imports: []ImportSpec{
			{Path: "github.com/spf13/cobra", Usage: "_ = cobra.Command{}"},
			{Path: "github.com/spf13/viper", Usage: "_ = viper.New()"},
			{Path: "github.com/urfave/cli/v2", Alias: "cli", Usage: "_ = cli.App{}"},
			{Path: "github.com/charmbracelet/bubbletea", Alias: "tea", Usage: "var _ tea.Model"},
			{Path: "github.com/charmbracelet/lipgloss", Usage: "_ = lipgloss.NewStyle()"},
			{Path: "github.com/fatih/color", Usage: "_ = color.New()"},
			{Path: "github.com/schollz/progressbar/v3", Alias: "progressbar", Usage: "_ = progressbar.Default(100)"},
			{Path: "github.com/manifoldco/promptui", Usage: "_ = promptui.Prompt{}"},
			{Path: "github.com/muesli/termenv", Usage: "_ = termenv.ColorProfile()"},
			{Path: "github.com/alecthomas/kong", Usage: "var _ *kong.Context"},
			{Path: "github.com/jessevdk/go-flags", Alias: "goflags", Usage: "var _ *goflags.Parser"},
		},
	},

	// 11. misc-heavy (~15)
	{
		Name: "misc-heavy",
		Imports: []ImportSpec{
			{Path: "github.com/hashicorp/consul/api", Alias: "consulapi", Usage: "_ = consulapi.DefaultConfig()"},
			{Path: "github.com/hashicorp/vault/api", Alias: "vaultapi", Usage: "_ = vaultapi.DefaultConfig()"},
			{Path: "github.com/nats-io/nats.go", Alias: "nats", Usage: "_ = nats.DefaultURL"},
			{Path: "github.com/rabbitmq/amqp091-go", Alias: "amqp", Usage: "_ = amqp.Config{}"},
			{Path: "github.com/minio/minio-go/v7", Alias: "minio", Usage: "_ = minio.Options{}"},
			{Path: "github.com/segmentio/kafka-go", Alias: "kafka", Usage: "_ = kafka.Message{}"},
			{Path: "github.com/opencontainers/image-spec/specs-go/v1", Alias: "ociv1", Usage: "_ = ociv1.Image{}"},
			{Path: "github.com/google/uuid", Usage: "_ = uuid.New()"},
			{Path: "github.com/cenkalti/backoff/v4", Alias: "backoff", Usage: "_ = backoff.NewExponentialBackOff()"},
			{Path: "go.uber.org/fx", Usage: "var _ *fx.App"},
			{Path: "go.uber.org/dig", Usage: "_ = dig.New()"},
			{Path: "golang.org/x/sync/errgroup", Alias: "errgroup", Usage: "_ = errgroup.Group{}"},
			{Path: "github.com/mitchellh/mapstructure", Usage: "_ = mapstructure.DecoderConfig{}"},
			{Path: "github.com/google/go-cmp/cmp", Usage: `_ = cmp.Diff("", "")`},
			{Path: "github.com/samber/lo", Usage: `_ = lo.Map([]int{1}, func(x int, _ int) int { return x })`},
			{Path: "github.com/hashicorp/go-multierror", Alias: "multierror", Usage: "var _ *multierror.Error"},
			{Path: "github.com/hashicorp/hcl/v2", Alias: "hcl", Usage: "_ = hcl.Pos{}"},
			{Path: "github.com/cespare/xxhash/v2", Alias: "xxhash", Usage: `_ = xxhash.Sum64String("")`},
			{Path: "github.com/shopspring/decimal", Usage: "_ = decimal.Zero"},
			{Path: "github.com/Masterminds/semver/v3", Alias: "semver", Usage: `_ = semver.MustParse("1.0.0")`},
			{Path: "golang.org/x/text/language", Usage: "_ = language.English"},
			{Path: "golang.org/x/time/rate", Usage: "_ = rate.Limit(0)"},
			{Path: "golang.org/x/net/http2", Alias: "http2", Usage: "_ = http2.Transport{}"},
		},
	},

	// 12. cloud-aws-extra (more AWS services, each a separate module)
	{
		Name: "cloud-aws-extra",
		Imports: []ImportSpec{
			{Path: "github.com/aws/aws-sdk-go-v2/service/rds", Usage: "var _ *rds.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/elasticache", Usage: "var _ *elasticache.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/kinesis", Usage: "var _ *kinesis.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/ses", Usage: "var _ *ses.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/ssm", Usage: "var _ *ssm.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/ecr", Usage: "var _ *ecr.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/efs", Usage: "var _ *efs.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/sfn", Usage: "var _ *sfn.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/cloudformation", Usage: "var _ *cloudformation.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/emr", Usage: "var _ *emr.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/glue", Usage: "var _ *glue.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/athena", Usage: "var _ *athena.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/redshift", Usage: "var _ *redshift.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/apigateway", Usage: "var _ *apigateway.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider", Usage: "var _ *cognitoidentityprovider.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/cloudfront", Usage: "var _ *cloudfront.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/waf", Usage: "var _ *waf.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/elasticsearchservice", Usage: "var _ *elasticsearchservice.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/firehose", Usage: "var _ *firehose.Client"},
			{Path: "github.com/aws/aws-sdk-go-v2/service/sagemaker", Usage: "var _ *sagemaker.Client"},
		},
	},

	// 13. cloud-gcp (heavy transitive deps)
	{
		Name: "cloud-gcp",
		Imports: []ImportSpec{
			{Path: "cloud.google.com/go/storage", Usage: "var _ *storage.Client"},
			{Path: "cloud.google.com/go/bigquery", Usage: "var _ *bigquery.Client"},
			{Path: "cloud.google.com/go/pubsub", Usage: "var _ *pubsub.Client"},
			{Path: "cloud.google.com/go/firestore", Usage: "var _ *firestore.Client"},
			{Path: "cloud.google.com/go/spanner", Usage: "var _ *spanner.Client"},
			{Path: "cloud.google.com/go/logging", Usage: "var _ *logging.Client"},
			{Path: "cloud.google.com/go/datastore", Usage: "var _ *datastore.Client"},
			{Path: "cloud.google.com/go/bigtable", Usage: "var _ *bigtable.Client"},
			{Path: "cloud.google.com/go/compute/metadata", Alias: "gcemetadata", Usage: "_ = gcemetadata.OnGCE()"},
			{Path: "cloud.google.com/go/iam", Alias: "cloudiam", Usage: "var _ *cloudiam.Handle"},
		},
	},

	// 14. cloud-azure
	{
		Name: "cloud-azure",
		Imports: []ImportSpec{
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/azcore", Usage: "var _ azcore.TokenCredential"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/azidentity", Usage: "var _ *azidentity.DefaultAzureCredential"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob", Usage: "var _ *azblob.Client"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs", Usage: "var _ *azeventhubs.ProducerClient"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos", Usage: "var _ *azcosmos.Client"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets", Usage: "var _ *azsecrets.Client"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6", Alias: "armcompute", Usage: "var _ *armcompute.VirtualMachinesClient"},
			{Path: "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6", Alias: "armnetwork", Usage: "var _ *armnetwork.InterfacesClient"},
		},
	},

	// 15. k8s-extra (more k8s API groups)
	{
		Name: "k8s-extra",
		Imports: []ImportSpec{
			{Path: "k8s.io/api/batch/v1", Alias: "batchv1", Usage: "_ = batchv1.Job{}"},
			{Path: "k8s.io/api/rbac/v1", Alias: "rbacv1", Usage: "_ = rbacv1.ClusterRole{}"},
			{Path: "k8s.io/api/networking/v1", Alias: "networkingv1", Usage: "_ = networkingv1.Ingress{}"},
			{Path: "k8s.io/api/storage/v1", Alias: "storagev1", Usage: "_ = storagev1.StorageClass{}"},
			{Path: "k8s.io/api/autoscaling/v2", Alias: "autoscalingv2", Usage: "_ = autoscalingv2.HorizontalPodAutoscaler{}"},
			{Path: "k8s.io/api/policy/v1", Alias: "policyv1", Usage: "_ = policyv1.PodDisruptionBudget{}"},
			{Path: "k8s.io/api/coordination/v1", Alias: "coordinationv1", Usage: "_ = coordinationv1.Lease{}"},
			{Path: "k8s.io/api/certificates/v1", Alias: "certificatesv1", Usage: "_ = certificatesv1.CertificateSigningRequest{}"},
			{Path: "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1", Alias: "apiextensionsv1", Usage: "_ = apiextensionsv1.CustomResourceDefinition{}"},
			{Path: "k8s.io/client-go/dynamic", Usage: "var _ dynamic.Interface"},
		},
	},

	// 16. orm-data
	{
		Name: "orm-data",
		Imports: []ImportSpec{
			{Path: "gorm.io/gorm", Usage: "var _ *gorm.DB"},
			{Path: "gorm.io/driver/postgres", Alias: "gormpostgres", Usage: "var _ = gormpostgres.Open"},
			{Path: "entgo.io/ent", Usage: "var _ ent.Value"},
			{Path: "entgo.io/ent/dialect/sql", Alias: "entsql", Usage: "var _ *entsql.Driver"},
			{Path: "github.com/go-gorm/caches/v4", Alias: "gormcaches", Usage: "_ = gormcaches.Config{}"},
			{Path: "github.com/huandu/go-sqlbuilder", Alias: "sqlbuilder", Usage: `_ = sqlbuilder.Select("")`},
			{Path: "github.com/doug-martin/goqu/v9", Alias: "goqu", Usage: `_ = goqu.Dialect("")`},
			{Path: "github.com/Masterminds/squirrel", Usage: `_ = squirrel.Select("")`},
			{Path: "github.com/georgysavva/scany/v2/pgxscan", Usage: "var _ = pgxscan.Select"},
			{Path: "github.com/jackc/pgx/v5/pgxpool", Usage: "var _ *pgxpool.Pool"},
		},
	},

	// 17. auth-security
	{
		Name: "auth-security",
		Imports: []ImportSpec{
			{Path: "golang.org/x/oauth2", Usage: "_ = oauth2.Token{}"},
			{Path: "golang.org/x/oauth2/google", Alias: "google", Usage: "var _ *google.Credentials"},
			{Path: "github.com/coreos/go-oidc/v3/oidc", Usage: "var _ *oidc.Provider"},
			{Path: "github.com/casbin/casbin/v2", Alias: "casbin", Usage: "var _ *casbin.Enforcer"},
			{Path: "github.com/go-ldap/ldap/v3", Alias: "ldap", Usage: "var _ *ldap.Conn"},
			{Path: "github.com/mikespook/gorbac", Usage: "_ = gorbac.New()"},
			{Path: "github.com/lestrrat-go/jwx/v2/jwt", Alias: "jwxjwt", Usage: "var _ jwxjwt.Token"},
			{Path: "github.com/lestrrat-go/jwx/v2/jws", Usage: "var _ *jws.Message"},
		},
	},

	// 18. data-processing
	{
		Name: "data-processing",
		Imports: []ImportSpec{
			{Path: "github.com/apache/arrow-go/v18/arrow", Usage: "var _ arrow.DataType"},
			{Path: "github.com/apache/arrow-go/v18/parquet", Usage: "var _ = parquet.Types"},
			{Path: "github.com/linkedin/goavro/v2", Alias: "goavro", Usage: "var _ *goavro.Codec"},
			{Path: "github.com/xitongsys/parquet-go/reader", Alias: "parquetreader", Usage: "var _ *parquetreader.ParquetReader"},
			{Path: "github.com/influxdata/influxdb-client-go/v2", Alias: "influxdb", Usage: "var _ influxdb.Client"},
			{Path: "github.com/ClickHouse/clickhouse-go/v2", Alias: "clickhouse", Usage: "var _ clickhouse.Conn"},
			{Path: "github.com/go-redis/redis_rate/v10", Alias: "redisrate", Usage: "var _ *redisrate.Limiter"},
			{Path: "github.com/allegro/bigcache/v3", Alias: "bigcache", Usage: "_ = bigcache.DefaultConfig(0)"},
			{Path: "github.com/patrickmn/go-cache", Alias: "gocache", Usage: "_ = gocache.New(0, 0)"},
			{Path: "github.com/coocood/freecache", Usage: "_ = freecache.NewCache(1024)"},
		},
	},

	// 19. networking-infra
	{
		Name: "networking-infra",
		Imports: []ImportSpec{
			{Path: "github.com/go-git/go-git/v5", Alias: "gogit", Usage: "var _ *gogit.Repository"},
			{Path: "github.com/hashicorp/memberlist", Usage: "_ = memberlist.DefaultLANConfig()"},
			{Path: "github.com/hashicorp/raft", Usage: "var _ raft.FSM"},
			{Path: "github.com/traefik/yaegi/interp", Usage: "_ = interp.New(interp.Options{})"},
			{Path: "github.com/go-playground/webhooks/v6/github", Alias: "ghwebhooks", Usage: "var _ = ghwebhooks.New"},
			{Path: "nhooyr.io/websocket", Alias: "nhws", Usage: "var _ *nhws.Conn"},
			{Path: "github.com/IBM/sarama", Usage: "_ = sarama.NewConfig()"},
			{Path: "github.com/twmb/franz-go/pkg/kgo", Usage: "var _ *kgo.Client"},
			{Path: "github.com/quic-go/quic-go", Alias: "quic", Usage: "var _ *quic.Config"},
			{Path: "github.com/pion/webrtc/v4", Alias: "webrtc", Usage: "_ = webrtc.Configuration{}"},
		},
	},

	// 20. testing-tools
	{
		Name: "testing-tools",
		Imports: []ImportSpec{
			{Path: "github.com/stretchr/testify/assert", Usage: "var _ = assert.Equal"},
			{Path: "github.com/stretchr/testify/require", Usage: "var _ = require.Equal"},
			{Path: "github.com/onsi/ginkgo/v2", Alias: "ginkgo", Usage: "var _ = ginkgo.Describe"},
			{Path: "github.com/onsi/gomega", Usage: "var _ = gomega.Expect"},
			{Path: "github.com/jarcoal/httpmock", Usage: "var _ = httpmock.Activate"},
			{Path: "github.com/DATA-DOG/go-sqlmock", Alias: "sqlmock", Usage: "var _ = sqlmock.New"},
			{Path: "github.com/golang/mock/gomock", Alias: "gomock", Usage: "var _ *gomock.Controller"},
			{Path: "github.com/brianvoe/gofakeit/v7", Alias: "gofakeit", Usage: "_ = gofakeit.Name()"},
			{Path: "github.com/google/go-github/v68/github", Alias: "ghapi", Usage: "_ = ghapi.NewClient(nil)"},
			{Path: "github.com/h2non/gock", Usage: "var _ = gock.New"},
		},
	},

	// 21. template-build
	{
		Name: "template-build",
		Imports: []ImportSpec{
			{Path: "html/template", Alias: "htmltemplate", Usage: "var _ *htmltemplate.Template"},
			{Path: "text/template", Alias: "texttemplate", Usage: "var _ *texttemplate.Template"},
			{Path: "github.com/Masterminds/sprig/v3", Alias: "sprig", Usage: "_ = sprig.FuncMap()"},
			{Path: "github.com/flosch/pongo2/v6", Alias: "pongo2", Usage: "var _ *pongo2.Template"},
			{Path: "github.com/valyala/quicktemplate", Usage: "var _ *quicktemplate.Writer"},
			{Path: "github.com/a-h/templ", Alias: "templ", Usage: "var _ templ.Component"},
			{Path: "github.com/magefile/mage/mg", Usage: "_ = mg.Verbose()"},
		},
	},

	// 22. more-observability (more exporters and backends)
	{
		Name: "more-observability",
		Imports: []ImportSpec{
			{Path: "go.opentelemetry.io/otel/exporters/prometheus", Alias: "otelprom", Usage: "var _ *otelprom.Exporter"},
			{Path: "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc", Usage: "var _ = otlpmetricgrpc.New"},
			{Path: "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc", Usage: "var _ = otlptracegrpc.New"},
			{Path: "go.opentelemetry.io/otel/sdk/metric", Alias: "sdkmetric", Usage: "var _ *sdkmetric.MeterProvider"},
			{Path: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", Usage: "var _ = otelhttp.NewHandler"},
			{Path: "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc", Usage: "var _ = otelgrpc.NewClientHandler"},
			{Path: "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl", Usage: "var _ *ottl.Parser[any]"},
			{Path: "github.com/prometheus/prometheus/model/labels", Alias: "promlabels", Usage: "_ = promlabels.Labels{}"},
			{Path: "github.com/gogo/protobuf/proto", Alias: "gogoproto", Usage: "var _ gogoproto.Message"},
			{Path: "github.com/prometheus/prometheus/model/histogram", Alias: "promhist", Usage: "_ = promhist.FloatHistogram{}"},
		},
	},

	// 23. ai-ml
	{
		Name: "ai-ml",
		Imports: []ImportSpec{
			{Path: "github.com/sashabaranov/go-openai", Alias: "openai", Usage: "_ = openai.ChatCompletionRequest{}"},
			{Path: "github.com/ollama/ollama/api", Alias: "ollamaapi", Usage: "_ = ollamaapi.ChatRequest{}"},
			{Path: "github.com/pgvector/pgvector-go", Alias: "pgvector", Usage: "_ = pgvector.NewVector(nil)"},
			{Path: "github.com/tmc/langchaingo/llms", Alias: "langchainllms", Usage: "var _ langchainllms.Model"},
			{Path: "github.com/milvus-io/milvus-sdk-go/v2/client", Alias: "milvusclient", Usage: "var _ milvusclient.Client"},
		},
	},

	// 24. config-env
	{
		Name: "config-env",
		Imports: []ImportSpec{
			{Path: "github.com/joho/godotenv", Usage: "var _ = godotenv.Load"},
			{Path: "github.com/kelseyhightower/envconfig", Usage: "var _ = envconfig.Process"},
			{Path: "github.com/knadh/koanf/v2", Alias: "koanf", Usage: `_ = koanf.New(".")`},
			{Path: "github.com/Netflix/go-env", Alias: "goenv", Usage: "var _ = goenv.UnmarshalFromEnviron"},
			{Path: "github.com/ilyakaznacheev/cleanenv", Usage: "var _ = cleanenv.ReadEnv"},
			{Path: "github.com/caarlos0/env/v11", Alias: "envparse", Usage: "var _ = envparse.Parse"},
		},
	},

	// 25. cgo (requires C libraries -- see nix/devshell.nix)
	{
		Name: "cgo",
		Imports: []ImportSpec{
			{Path: "github.com/davidbyttow/govips/v2/vips", Usage: "var _ *vips.ImageRef"},
			{Path: "github.com/linxGnu/grocksdb", Usage: "_ = grocksdb.NewDefaultOptions()"},
			{Path: "github.com/google/gopacket/pcap", Usage: "_ = pcap.BlockForever"},
			{Path: "gopkg.in/gographics/imagick.v3/imagick", Usage: "var _ *imagick.MagickWand"},
			{Path: "github.com/gordonklaus/portaudio", Usage: "var _ = portaudio.Initialize"},
		},
	},

	// 26. workflow-scheduling
	{
		Name: "workflow-scheduling",
		Imports: []ImportSpec{
			{Path: "github.com/robfig/cron/v3", Alias: "cron", Usage: "_ = cron.New()"},
			{Path: "github.com/hibiken/asynq", Usage: "_ = asynq.RedisClientOpt{}"},
			{Path: "github.com/riverqueue/river", Usage: "var _ *river.Client[any]"},
			{Path: "github.com/go-co-op/gocron/v2", Alias: "gocron", Usage: "var _ gocron.Scheduler"},
			{Path: "github.com/reugn/go-quartz/quartz", Usage: "var _ quartz.Scheduler"},
			{Path: "go.temporal.io/sdk/client", Alias: "temporalclient", Usage: "var _ temporalclient.Client"},
			{Path: "go.temporal.io/sdk/workflow", Alias: "temporalwf", Usage: "var _ = temporalwf.Go"},
		},
	},
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func categoryByName(name string) *DepCategory {
	for i := range categories {
		if categories[i].Name == name {
			return &categories[i]
		}
	}
	return nil
}

func depsForModule(mod ModuleDef) []ImportSpec {
	var out []ImportSpec
	for _, catName := range mod.Categories {
		cat := categoryByName(catName)
		if cat != nil {
			out = append(out, cat.Imports...)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Generation
// ---------------------------------------------------------------------------

func generateGoMod(baseDir string, mod ModuleDef) error {
	modPath := modulePath + "/internal/" + mod.Name
	var buf strings.Builder
	fmt.Fprintf(&buf, "module %s\n\ngo 1.25\n", modPath)

	if len(mod.LocalDeps) > 0 {
		buf.WriteString("\nrequire (\n")
		for _, dep := range mod.LocalDeps {
			fmt.Fprintf(&buf, "\t%s/internal/%s v0.0.0\n", modulePath, dep)
		}
		buf.WriteString(")\n")
	}

	// replace directives: local deps + version pins
	hasReplaces := len(mod.LocalDeps) > 0 || len(mod.Replaces) > 0
	if hasReplaces {
		buf.WriteString("\nreplace (\n")
		for _, dep := range mod.LocalDeps {
			fmt.Fprintf(&buf, "\t%s/internal/%s => ../%s\n", modulePath, dep, dep)
		}
		for pkg, ver := range mod.Replaces {
			fmt.Fprintf(&buf, "\t%s => %s %s\n", pkg, pkg, ver)
		}
		buf.WriteString(")\n")
	}

	path := filepath.Join(baseDir, "internal", mod.Name, "go.mod")
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// AppDef describes an application module in the generated project.
type AppDef struct {
	Name     string
	Libs     []string // names of internal modules to depend on; nil = all
	SkipTidy bool     // if true, don't run go mod tidy (tests untidy scenario)
}

var apps = []AppDef{
	{Name: "app-full", Libs: nil},
	{Name: "app-partial", Libs: []string{"common", "aws", "db", "crypto", "conflict-a"}},
	{Name: "app-untidy", Libs: nil, SkipTidy: true},
}

func libsForApp(app AppDef) []ModuleDef {
	if app.Libs == nil {
		return multiModules
	}
	var out []ModuleDef
	for _, name := range app.Libs {
		for _, m := range multiModules {
			if m.Name == name {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func generateAppGoMod(baseDir string, app AppDef) error {
	modPath := modulePath + "/" + app.Name
	libs := libsForApp(app)
	var buf strings.Builder
	fmt.Fprintf(&buf, "module %s\n\ngo 1.25\n", modPath)

	buf.WriteString("\nrequire (\n")
	for _, mod := range libs {
		fmt.Fprintf(&buf, "\t%s/internal/%s v0.0.0\n", modulePath, mod.Name)
	}
	buf.WriteString(")\n")

	// Collect version-pinning replaces from deps; when multiple deps pin
	// the same package, keep the higher version (mimics MVS).
	versionPins := map[string]string{}
	for _, mod := range libs {
		for pkg, ver := range mod.Replaces {
			if existing, ok := versionPins[pkg]; !ok || ver > existing {
				versionPins[pkg] = ver
			}
		}
	}

	buf.WriteString("\nreplace (\n")
	for _, mod := range libs {
		fmt.Fprintf(&buf, "\t%s/internal/%s => ../internal/%s\n", modulePath, mod.Name, mod.Name)
	}
	for pkg, ver := range versionPins {
		fmt.Fprintf(&buf, "\t%s => %s %s\n", pkg, pkg, ver)
	}
	buf.WriteString(")\n")

	path := filepath.Join(baseDir, app.Name, "go.mod")
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

func generateLibSource(baseDir string, mod ModuleDef) error {
	deps := depsForModule(mod)

	needsContext := false
	for _, d := range deps {
		if strings.Contains(d.Usage, "context.") {
			needsContext = true
			break
		}
	}

	var buf strings.Builder
	buf.WriteString("// Code generated by generator; DO NOT EDIT.\n\n")
	buf.WriteString("package " + mod.PkgName + "\n\n")
	buf.WriteString("import (\n")
	if needsContext {
		buf.WriteString("\t\"context\"\n")
	}
	for _, dep := range mod.LocalDeps {
		for _, m := range multiModules {
			if m.Name == dep {
				fmt.Fprintf(&buf, "\t%s %q\n", m.PkgName, modulePath+"/internal/"+dep)
				break
			}
		}
	}
	for _, d := range deps {
		if d.Alias != "" {
			fmt.Fprintf(&buf, "\t%s %q\n", d.Alias, d.Path)
		} else {
			fmt.Fprintf(&buf, "\t%q\n", d.Path)
		}
	}
	buf.WriteString(")\n\n")

	buf.WriteString("// Run exercises imported packages.\n")
	buf.WriteString("func Run() error {\n")
	if needsContext {
		buf.WriteString("\t_ = context.Background()\n")
	}
	for _, dep := range mod.LocalDeps {
		for _, m := range multiModules {
			if m.Name == dep {
				fmt.Fprintf(&buf, "\tif err := %s.Run(); err != nil {\n\t\treturn err\n\t}\n", m.PkgName)
				break
			}
		}
	}
	for _, d := range deps {
		buf.WriteString("\t" + d.Usage + "\n")
	}
	buf.WriteString("\treturn nil\n")
	buf.WriteString("}\n")

	src, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("format %s: %w\nsource:\n%s", mod.Name, err, buf.String())
	}

	path := filepath.Join(baseDir, "internal", mod.Name, mod.PkgName+".go")
	return os.WriteFile(path, src, 0o644)
}

func generateAppMain(baseDir string, app AppDef) error {
	libs := libsForApp(app)
	var buf strings.Builder
	buf.WriteString("// Code generated by generator; DO NOT EDIT.\n\n")
	buf.WriteString("package main\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\t\"fmt\"\n")
	buf.WriteString("\t\"os\"\n\n")
	for _, mod := range libs {
		fmt.Fprintf(&buf, "\t%s %q\n", mod.PkgName, modulePath+"/internal/"+mod.Name)
	}
	buf.WriteString(")\n\n")

	buf.WriteString("func main() {\n")
	for _, mod := range libs {
		fmt.Fprintf(&buf, "\tif err := %s.Run(); err != nil {\n", mod.PkgName)
		fmt.Fprintf(&buf, "\t\tfmt.Fprintf(os.Stderr, \"%s.Run: %%v\\n\", err)\n", mod.PkgName)
		buf.WriteString("\t\tos.Exit(1)\n")
		buf.WriteString("\t}\n")
	}
	buf.WriteString("\tfmt.Println(\"ok\")\n")
	buf.WriteString("}\n")

	src, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("format %s/main.go: %w\nsource:\n%s", app.Name, err, buf.String())
	}

	dir := filepath.Join(baseDir, app.Name, "cmd", app.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "main.go"), src, 0o644)
}

// generateGoWork creates a go.work file that unifies all modules into a single
// Go workspace. Required for Gazelle's go_deps module extension.
//
// When multiple modules have conflicting version-pin replaces (e.g. conflict-a
// pins gin@v1.9.1, conflict-b pins gin@v1.10.0), the workspace resolves them
// by picking the higher version (MVS-like).
func generateGoWork(baseDir string) error {
	var buf strings.Builder
	buf.WriteString("go 1.25\n\nuse (\n")
	for _, app := range apps {
		if app.SkipTidy {
			continue // untidy apps have no go.sum, can't be part of workspace
		}
		fmt.Fprintf(&buf, "\t./%s\n", app.Name)
	}
	for _, mod := range multiModules {
		fmt.Fprintf(&buf, "\t./internal/%s\n", mod.Name)
	}
	buf.WriteString(")\n")

	// Collect version-pin replaces across all modules; when multiple
	// modules pin the same package to different versions, keep the higher
	// one. These workspace-level replaces resolve conflicts that `go work
	// sync` would otherwise reject.
	pins := map[string]string{}
	var count int
	for _, mod := range multiModules {
		for pkg, ver := range mod.Replaces {
			if existing, ok := pins[pkg]; !ok || ver > existing {
				pins[pkg] = ver
			}
			count++
		}
	}
	// Only emit replace block if there are actual conflicts (i.e. more
	// pins than unique packages, meaning at least two modules pin the
	// same package).
	if count > len(pins) && len(pins) > 0 {
		sorted := make([]string, 0, len(pins))
		for pkg := range pins {
			sorted = append(sorted, pkg)
		}
		sort.Strings(sorted)
		buf.WriteString("\n// Resolve conflicting version-pin replaces across modules.\n")
		buf.WriteString("replace (\n")
		for _, pkg := range sorted {
			fmt.Fprintf(&buf, "\t%s => %s %s\n", pkg, pkg, pins[pkg])
		}
		buf.WriteString(")\n")
	}

	return os.WriteFile(filepath.Join(baseDir, "go.work"), []byte(buf.String()), 0o644)
}

// generateBazelFiles creates the Bazel workspace scaffolding: MODULE.bazel,
// root BUILD.bazel, .bazelversion, and .bazelrc. Per-module BUILD.bazel files
// are generated afterwards by Gazelle (bazel run @gazelle//:gazelle).
func generateBazelFiles(baseDir string) error {
	// .bazelversion — pin for reproducible benchmarks
	if err := os.WriteFile(filepath.Join(baseDir, ".bazelversion"), []byte("7.6.0\n"), 0o644); err != nil {
		return fmt.Errorf(".bazelversion: %w", err)
	}

	// .bazelrc — benchmark-friendly defaults
	bazelrc := `# Disable remote cache for fair benchmarking
build --remote_cache=
build --disk_cache=

# Sandboxed execution (matches Nix sandbox)
build --spawn_strategy=sandboxed

# Hermetic environment
build --incompatible_strict_action_env
`
	if err := os.WriteFile(filepath.Join(baseDir, ".bazelrc"), []byte(bazelrc), 0o644); err != nil {
		return fmt.Errorf(".bazelrc: %w", err)
	}

	// Root BUILD.bazel with custom Gazelle binary (avoids the upstream
	// gazelle_local target's dev-only dependency on bazel_skylib_gazelle_plugin).
	buildBazel := `load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")

# gazelle:prefix github.com/numtide/go2nix/torture

gazelle_binary(
    name = "gazelle_bin",
    languages = [
        "@gazelle//language/proto",
        "@gazelle//language/go",
    ],
)

gazelle(
    name = "gazelle",
    gazelle = ":gazelle_bin",
)
`
	if err := os.WriteFile(filepath.Join(baseDir, "BUILD.bazel"), []byte(buildBazel), 0o644); err != nil {
		return fmt.Errorf("BUILD.bazel: %w", err)
	}

	// MODULE.bazel — bzlmod root with rules_go + gazelle
	//
	// The go_deps extension reads the go.work file to resolve all external
	// Go dependencies. After generation, run `bazel mod tidy` to populate
	// the use_repo(...) directive with the full list of ~497 repos.
	var buf strings.Builder
	buf.WriteString(`module(name = "torture_project", version = "0.0.0")

bazel_dep(name = "rules_go", version = "0.60.0")
bazel_dep(name = "gazelle", version = "0.42.0")

go_sdk = use_extension("@rules_go//go:extensions.bzl", "go_sdk")
go_sdk.host()

go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_work = "//:go.work")

# etcd's api/v3 module has proto-generated dirs without BUILD files.
go_deps.gazelle_override(
    path = "go.etcd.io/etcd/api/v3",
    directives = [
        "gazelle:proto disable",
    ],
    build_file_generation = "on",
)
`)

	// Collect all external module paths that appear in replace directives
	// so they're available as repos. The full list is populated by
	// `bazel mod tidy`, but we seed the version-pinned ones here.
	pinned := map[string]bool{}
	for _, mod := range multiModules {
		for pkg := range mod.Replaces {
			pinned[pkg] = true
		}
	}
	if len(pinned) > 0 {
		sorted := make([]string, 0, len(pinned))
		for pkg := range pinned {
			sorted = append(sorted, pkg)
		}
		sort.Strings(sorted)
		buf.WriteString("\n# Version-pinned repos from replace directives.\n")
		buf.WriteString("# Run `bazel mod tidy` to populate the full use_repo list.\n")
		buf.WriteString("use_repo(\n    go_deps,\n")
		for _, pkg := range sorted {
			// Convert Go module path to Bazel repo name: replace / and . with _
			repo := goModuleToRepo(pkg)
			fmt.Fprintf(&buf, "    %q,\n", repo)
		}
		buf.WriteString(")\n")
	}

	if err := os.WriteFile(filepath.Join(baseDir, "MODULE.bazel"), []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("MODULE.bazel: %w", err)
	}

	return nil
}

// goModuleToRepo converts a Go module path to a Bazel repository name
// following the gazelle convention: com_github_foo_bar for github.com/foo/bar.
func goModuleToRepo(modPath string) string {
	// Strip version suffix (e.g. /v5, /v9)
	parts := strings.Split(modPath, "/")
	var filtered []string
	for _, p := range parts {
		if len(p) > 1 && p[0] == 'v' && p[1] >= '0' && p[1] <= '9' {
			continue
		}
		filtered = append(filtered, p)
	}
	// Reverse domain parts and join with underscore
	// github.com/foo/bar -> com_github_foo_bar
	if len(filtered) > 1 {
		domain := strings.Split(filtered[0], ".")
		for i, j := 0, len(domain)-1; i < j; i, j = i+1, j-1 {
			domain[i], domain[j] = domain[j], domain[i]
		}
		filtered[0] = strings.Join(domain, "_")
	}
	result := strings.Join(filtered, "_")
	// Replace hyphens with underscores
	result = strings.ReplaceAll(result, "-", "_")
	return result
}

// RunTorture generates a multi-module torture-test Go project in baseDir.
func RunTorture(baseDir string) error {
	fmt.Printf("Generating multi-module layout with %d lib modules + app into %s/...\n",
		len(multiModules), baseDir)

	for _, mod := range multiModules {
		dir := filepath.Join(baseDir, "internal", mod.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	for _, app := range apps {
		if err := os.MkdirAll(filepath.Join(baseDir, app.Name), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", app.Name, err)
		}
	}

	for _, mod := range multiModules {
		if err := generateGoMod(baseDir, mod); err != nil {
			return fmt.Errorf("go.mod %s: %w", mod.Name, err)
		}
		if err := generateLibSource(baseDir, mod); err != nil {
			return fmt.Errorf("source %s: %w", mod.Name, err)
		}
		fmt.Printf("  Generated %s (%d external deps, %d local deps)\n",
			mod.Name, len(depsForModule(mod)), len(mod.LocalDeps))
	}

	for _, app := range apps {
		libs := libsForApp(app)
		if err := generateAppGoMod(baseDir, app); err != nil {
			return fmt.Errorf("go.mod %s: %w", app.Name, err)
		}
		if err := generateAppMain(baseDir, app); err != nil {
			return fmt.Errorf("source %s: %w", app.Name, err)
		}
		fmt.Printf("  Generated %s (depends on %d libs)\n", app.Name, len(libs))
	}

	// Generate Go workspace file for Bazel/Gazelle integration.
	if err := generateGoWork(baseDir); err != nil {
		return fmt.Errorf("go.work: %w", err)
	}
	fmt.Println("  Generated go.work")

	// Generate Bazel workspace scaffolding.
	if err := generateBazelFiles(baseDir); err != nil {
		return fmt.Errorf("bazel: %w", err)
	}
	fmt.Println("  Generated Bazel scaffolding (MODULE.bazel, BUILD.bazel, .bazelversion, .bazelrc)")

	fmt.Printf("Done. Run go mod tidy on internal modules and apps (skip: ")
	for _, app := range apps {
		if app.SkipTidy {
			fmt.Printf("%s ", app.Name)
		}
	}
	fmt.Printf(")\nThen run: cd %s && bazel run @gazelle//:gazelle && bazel mod tidy\n", baseDir)
	return nil
}
