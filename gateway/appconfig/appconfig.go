package appconfig

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// TODO: it should include all runtime configuration

const (
	defaultPostgRESTRole = "hoop_apiuser"
)

type pgCredentials struct {
	connectionString string
	username         string
	// Postgrest Role Name
	postgrestRole string
}
type Config struct {
	askAICredentials   *url.URL
	pgCred             *pgCredentials
	migrationPathFiles string

	isLoaded bool
}

var runtimeConfig Config

// Load validate for any errors and set the RuntimeConfig var
func Load() error {
	askAICred, err := loadAskAICredentials()
	if err != nil {
		return err
	}
	pgCred, err := loadPostgresCredentials()
	if err != nil {
		return err
	}
	pgCred.postgrestRole = os.Getenv("PGREST_ROLE")
	if pgCred.postgrestRole == "" {
		pgCred.postgrestRole = defaultPostgRESTRole
	}
	migrationPathFiles := strings.TrimSuffix(os.Getenv("MIGRATION_PATH_FILES"), "/")
	if migrationPathFiles == "" {
		migrationPathFiles = "/app/migrations"
	}
	firstMigrationFilePath := fmt.Sprintf("%s/000001_init.up.sql", migrationPathFiles)
	if _, err := os.Stat(firstMigrationFilePath); err != nil {
		return fmt.Errorf("unable to find first migration file %v, err=%v", firstMigrationFilePath, err)
	}
	runtimeConfig = Config{
		askAICredentials:   askAICred,
		pgCred:             pgCred,
		migrationPathFiles: migrationPathFiles,
		isLoaded:           true,
	}
	return nil
}

func Get() Config { return runtimeConfig }
func loadPostgresCredentials() (*pgCredentials, error) {
	pgConnectionURI := os.Getenv("POSTGRES_DB_URI")
	pgURL, err := url.Parse(pgConnectionURI)
	if err != nil {
		return nil, fmt.Errorf("failed parsing POSTGRES_DB_URI, err=%v", err)
	}

	var pgUser, pgPassword string
	if pgURL.User != nil {
		pgUser = pgURL.User.Username()
		pgPassword, _ = pgURL.User.Password()
	}
	if pgUser == "" || pgPassword == "" {
		return nil, fmt.Errorf("missing user or password in POSTGRES_DB_URI env")
	}
	return &pgCredentials{connectionString: pgConnectionURI, username: pgUser}, nil
}

func loadAskAICredentials() (*url.URL, error) {
	askAICred := os.Getenv("ASK_AI_CREDENTIALS")
	if askAICred == "" {
		return nil, nil
	}
	u, err := url.Parse(askAICred)
	if err != nil {
		return nil, fmt.Errorf("ASK_AI_CREDENTIALS env is not in a valid configuration format: %v", err)
	}
	if u.User == nil {
		return nil, fmt.Errorf("ASK_AI_CREDENTIALS env is missing the api key")
	}
	if apiKey, _ := u.User.Password(); apiKey == "" {
		return nil, fmt.Errorf("ASK_AI_CREDENTIALS env is missing the api key")
	}
	return u, nil
}

func (c Config) PgUsername() string    { return c.pgCred.username }
func (c Config) PgURI() string         { return c.pgCred.connectionString }
func (c Config) PostgRESTRole() string { return c.pgCred.postgrestRole }

func (c Config) MigrationPathFiles() string { return c.migrationPathFiles }

func (c Config) IsAskAIAvailable() bool { return c.askAICredentials != nil }
func (c Config) AskAIApiURL() (u string) {
	if c.IsAskAIAvailable() {
		return fmt.Sprintf("%s://%s", c.askAICredentials.Scheme, c.askAICredentials.Host)
	}
	return ""
}

func (c Config) AskAIAPIKey() (t string) {
	if c.IsAskAIAvailable() {
		t, _ = c.askAICredentials.User.Password()
	}
	return
}
