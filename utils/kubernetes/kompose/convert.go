package kompose

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/kubernetes/kompose/client"
	"github.com/meshery/meshkit/utils"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// convertMu serializes calls to Convert. Convert swaps the process-global
// logrus ExitFunc for the duration of a call and restores it on return,
// which is not safe to do from concurrent goroutines: a call that has
// already restored the real ExitFunc lets a fatal log in another in-flight
// conversion exit the process. Holding the mutex for the duration of the
// conversion keeps the swap and its restore atomic with respect to other
// conversions until the override can be removed altogether.
var convertMu sync.Mutex

// Pinned to an immutable ref: validation semantics must not drift with the
// upstream master branch (its 2026-07-03 schema update made empty manifests
// pass validation). Bump deliberately, re-running the kompose package tests.
const DefaultDockerComposeSchemaURL = "https://raw.githubusercontent.com/compose-spec/compose-spec/a08f8d51631b7127ddac3d2c65537db429d0f1d0/schema/compose-spec.json"

// Checks whether the given manifest is a valid docker-compose file.
// schemaURL is assigned a default url if not specified
// error will be 'nil' if it is a valid docker compose file
func IsManifestADockerCompose(manifest []byte, schemaURL string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ErrValidateDockerComposeFile(fmt.Errorf("panic: %v", r))
		}
	}()

	if schemaURL == "" {
		schemaURL = DefaultDockerComposeSchemaURL
	}
	schema, err := utils.ReadRemoteFile(schemaURL)
	if err != nil {
		return err
	}
	var dockerComposeFile DockerComposeFile = manifest
	err = dockerComposeFile.Validate([]byte(schema))
	return err
}

// converts a given docker-compose file into kubernetes manifests
// expects a validated docker-compose file
func Convert(dockerCompose DockerComposeFile) (result string, err error) {
	convertMu.Lock()
	defer convertMu.Unlock()

  exitFunc := logrus.StandardLogger().ExitFunc;
	// revert back to original exit func for logrus
	defer func() {
		logrus.StandardLogger().ExitFunc = exitFunc
	}()

	// temporarly disable the default exit function ( os.exit) , because kompose using fatal logging rather than returning errors
	logrus.StandardLogger().ExitFunc = func (exitCode int){
		logrus.Errorf("Failed to convert dockercompose to manifest %v",exitCode)
	}

	defer func() {
		if r := recover(); r != nil {
		  fmt.Println("paniced will converting dockercompose to go")
		}
	}()


	defer func() {
		if r := recover(); r != nil {
			result = ""
			err = ErrCvrtKompose(fmt.Errorf("panic: %v", r))
		}
	}()

	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", ErrCvrtKompose(err)
	}

	// Construct path to .meshery directory
	mesheryDir := filepath.Join(homeDir, ".meshery")

	// Give this conversion a working directory of its own. The input, env
	// and result files used to sit at fixed paths directly in mesheryDir,
	// so concurrent conversions overwrote, appended to and deleted each
	// other's files.
	workDir, err := os.MkdirTemp(mesheryDir, "kompose-")
	if err != nil {
		return "", ErrCvrtKompose(err)
	}
	defer func() {
		_ = os.RemoveAll(workDir)
	}()

	tempFilePath := filepath.Join(workDir, "temp.data")
	resultFilePath := filepath.Join(workDir, "result.yaml")

	// create a empty .env file to avoid fatal error from kompose while reading env file
	if err := utils.CreateFile([]byte(""), ".env", workDir); err != nil {
		return "", ErrCvrtKompose(err)
	}

	if err := utils.CreateFile(dockerCompose, "temp.data", workDir); err != nil {
		return "", ErrCvrtKompose(err)
	}

	formatComposeFile(&dockerCompose)
	err = versionCheck(dockerCompose)
	if err != nil {
		return "", ErrCvrtKompose(err)
	}

	komposeOpts := client.WithSuppressWarnings()
	komposeClient, err := client.NewClient(komposeOpts)
	
	if err != nil {
		return "", ErrCvrtKompose(err)
	}

	ConvertOpt := client.ConvertOptions{
		InputFiles:              []string{tempFilePath},
		ToStdout: false,
		OutFile:                 resultFilePath,
		GenerateNetworkPolicies: true,
	}

	

	_, err = komposeClient.Convert(ConvertOpt)
	if err != nil {
		return "", ErrCvrtKompose(err)
	}

	resultBytes, err := os.ReadFile(resultFilePath)

	if len(resultBytes) == 0{
		return "", ErrCvrtKompose(err)
	}


	if err != nil {
		return "", ErrCvrtKompose(err)
	}

	return string(resultBytes), nil
}

type composeFile struct {
	Version string `yaml:"version,omitempty"`
}

// checks if the version is compatible with `kompose`
// expects a valid docker compose yaml
// error = nil means it is compatible
func versionCheck(dc DockerComposeFile) error {
	cf := composeFile{}
	err := yaml.Unmarshal(dc, &cf)
	if err != nil {
		return utils.ErrUnmarshal(err)
	}
	// assume compatible if version is not specified
	if cf.Version == "" {
		return nil
	}
	versionFloatVal, err := strconv.ParseFloat(cf.Version, 64)
	if err != nil {
		return utils.ErrExpectedTypeMismatch(err, "float")
	} else {
		if versionFloatVal > 3.9 {
			return ErrIncompatibleVersion()
		}
	}
	return nil
}

// formatComposeFile takes in a pointer to the compose file byte array and formats it so that it is compatible with `Kompose`
// it expects a validated docker compose file and does not validate
func formatComposeFile(yamlManifest *DockerComposeFile) {
	data := composeFile{}
	err := yaml.Unmarshal(*yamlManifest, &data)
	if err != nil {
		return
	}
	// so that "3.3" and 3.3 are treated differently by `Kompose`
	out, err := yaml.Marshal(data)
	if err != nil {
		return
	}
	*yamlManifest = out
}
