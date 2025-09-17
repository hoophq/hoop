package daemon

import "github.com/hoophq/hoop/common/log"

func EnvFilesAgent() error {
	envPath, err := envFileExist()
	if err != nil {
		log.Errorf("No existing hoop.conf file found: %v", err)
		return err
	}
	log.Infof("Using existing hoop.conf file at %s", envPath)
	envKeys, err := LoadEnvFile(envPath)
	if err != nil {
		return err
	}
	for k, v := range envKeys {
		log.Infof("Loaded env var %s=%s", k, v)
	}
	return nil
}
