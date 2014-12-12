package configuration

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/BrianBland/yaml.v2"
)

// Configuration is a versioned registry configuration, intended to be provided by a yaml file, and
// optionally modified by environment variables
type Configuration struct {
	// Version is the version which defines the format of the rest of the configuration
	Version Version `yaml:"version"`

	// Loglevel is the level at which registry operations are logged
	Loglevel Loglevel `yaml:"loglevel"`

	// Storage is the configuration for the registry's storage driver
	Storage Storage `yaml:"storage"`

	Auth Auth `yaml:"auth"`

	// HTTP contains configuration parameters for the registry's http
	// interface.
	HTTP struct {
		// Addr specifies the bind address for the registry instance.
		Addr string `yaml:"addr"`
	} `yaml:"http"`
}

// v0_1Configuration is a Version 0.1 Configuration struct
// This is currently aliased to Configuration, as it is the current version
type v0_1Configuration Configuration

// Version is a major/minor version pair of the form Major.Minor
// Major version upgrades indicate structure or type changes
// Minor version upgrades should be strictly additive
type Version string

// MajorMinorVersion constructs a Version from its Major and Minor components
func MajorMinorVersion(major, minor uint) Version {
	return Version(fmt.Sprintf("%d.%d", major, minor))
}

func (version Version) major() (uint, error) {
	majorPart := strings.Split(string(version), ".")[0]
	major, err := strconv.ParseUint(majorPart, 10, 0)
	return uint(major), err
}

// Major returns the major version portion of a Version
func (version Version) Major() uint {
	major, _ := version.major()
	return major
}

func (version Version) minor() (uint, error) {
	minorPart := strings.Split(string(version), ".")[1]
	minor, err := strconv.ParseUint(minorPart, 10, 0)
	return uint(minor), err
}

// Minor returns the minor version portion of a Version
func (version Version) Minor() uint {
	minor, _ := version.minor()
	return minor
}

// UnmarshalYAML implements the yaml.Unmarshaler interface
// Unmarshals a string of the form X.Y into a Version, validating that X and Y can represent uints
func (version *Version) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var versionString string
	err := unmarshal(&versionString)
	if err != nil {
		return err
	}

	newVersion := Version(versionString)
	if _, err := newVersion.major(); err != nil {
		return err
	}

	if _, err := newVersion.minor(); err != nil {
		return err
	}

	*version = newVersion
	return nil
}

// CurrentVersion is the most recent Version that can be parsed
var CurrentVersion = MajorMinorVersion(0, 1)

// Loglevel is the level at which operations are logged
// This can be error, warn, info, or debug
type Loglevel string

// UnmarshalYAML implements the yaml.Umarshaler interface
// Unmarshals a string into a Loglevel, lowercasing the string and validating that it represents a
// valid loglevel
func (loglevel *Loglevel) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var loglevelString string
	err := unmarshal(&loglevelString)
	if err != nil {
		return err
	}

	loglevelString = strings.ToLower(loglevelString)
	switch loglevelString {
	case "error", "warn", "info", "debug":
	default:
		return fmt.Errorf("Invalid loglevel %s Must be one of [error, warn, info, debug]", loglevelString)
	}

	*loglevel = Loglevel(loglevelString)
	return nil
}

// typedParemeters a type that exposes the key along with child values in a
// yaml file.
type typedParameters map[string]Parameters

// Type returns the parameters type, such as filesystem or s3, or basic or
// bearer for auth.
func (tp typedParameters) Type() string {
	// Return only key in this map
	for k := range tp {
		return k
	}
	return ""
}

// Parameters returns the Parameters map for a parameterized configuration
func (tp typedParameters) Parameters() Parameters {
	return tp[tp.Type()]
}

// setParameter changes the parameter at the provided key to the new value.
func (tp typedParameters) setParameter(key, value string) {
	tp[tp.Type()][key] = value
}

// UnmarshalYAML implements the yaml.Unmarshaler interface. Unmarshals a
// single item map into a typedParemeter or a string into a type with no
// parameters
func (tp *typedParameters) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var m map[string]Parameters
	err := unmarshal(&m)
	if err == nil {
		if len(m) > 1 {
			types := make([]string, 0, len(m))
			for k := range m {
				types = append(types, k)
			}

			// TODO(stevvooe): May want to change this slightly for
			// authorization to allow multiple challenges.
			return fmt.Errorf("must provide exactly one type. Provided: %v", types)
		}
		*tp = m
		return nil
	}

	var typ string
	err = unmarshal(&typ)
	if err == nil {
		*tp = typedParameters{typ: Parameters{}}
		return nil
	}

	return err
}

// MarshalYAML implements the yaml.Marshaler interface
func (tp typedParameters) MarshalYAML() (interface{}, error) {
	if tp.Parameters() == nil {
		return tp.Type(), nil
	}
	return map[string]Parameters(tp), nil
}

// Storage defines the configuration for registry object storage
type Storage struct {
	typedParameters
}

// Auth defines the configuration for registry authorization and access
// control.
type Auth struct {
	typedParameters
}

// Parameters defines a key-value parameters mapping
type Parameters map[string]string

// Parse parses an input configuration yaml document into a Configuration struct
// This should generally be capable of handling old configuration format versions
//
// Environment variables may be used to override configuration parameters other than version,
// following the scheme below:
// Configuration.Abc may be replaced by the value of REGISTRY_ABC,
// Configuration.Abc.Xyz may be replaced by the value of REGISTRY_ABC_XYZ, and so forth
func Parse(rd io.Reader) (*Configuration, error) {
	in, err := ioutil.ReadAll(rd)
	if err != nil {
		return nil, err
	}

	var untypedConfig struct {
		Version Version
	}
	var config *Configuration

	if err := yaml.Unmarshal(in, &untypedConfig); err != nil {
		return nil, err
	}

	if untypedConfig.Version == "" {
		return nil, fmt.Errorf("Please specify a configuration version. Current version is %s", CurrentVersion)
	}

	// Parse the remainder of the configuration depending on the provided version
	switch untypedConfig.Version {
	case "0.1":
		config, err = parseV0_1Registry(in)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("Unsupported configuration version %s Current version is %s", untypedConfig.Version, CurrentVersion)
	}

	return config, nil
}

// parseV0_1Registry parses a registry Configuration for Version 0.1
func parseV0_1Registry(in []byte) (*Configuration, error) {
	envMap := getEnvMap()

	var config v0_1Configuration
	err := yaml.Unmarshal(in, &config)
	if err != nil {
		return nil, err
	}

	// Override config.Loglevel if environment variable is provided
	if loglevel, ok := envMap["REGISTRY_LOGLEVEL"]; ok {
		var newLoglevel Loglevel
		err := yaml.Unmarshal([]byte(loglevel), &newLoglevel)
		if err != nil {
			return nil, err
		}
		config.Loglevel = newLoglevel
	}

	// Override config.Storage if environment variable is provided
	if storageType, ok := envMap["REGISTRY_STORAGE"]; ok {
		if storageType != config.Storage.Type() {
			// Reset the storage parameters because we're using a different storage type
			config.Storage = Storage{typedParameters: typedParameters{storageType: Parameters{}}}
		}
	}

	if config.Storage.Type() == "" {
		return nil, fmt.Errorf("Must provide exactly one storage type, optionally with parameters. Provided: %v", config.Storage)
	}

	// Override storage parameters with all environment variables of the format:
	// REGISTRY_STORAGE_<storage driver type>_<parameter name>
	storageParamsRegexp, err := regexp.Compile(fmt.Sprintf("^REGISTRY_STORAGE_%s_([A-Z0-9]+)$", strings.ToUpper(config.Storage.Type())))
	if err != nil {
		return nil, err
	}
	for k, v := range envMap {
		if submatches := storageParamsRegexp.FindStringSubmatch(k); submatches != nil {
			config.Storage.setParameter(strings.ToLower(submatches[1]), v)
		}
	}

	return (*Configuration)(&config), nil
}

// getEnvMap reads the current environment variables and converts these into a key/value map
// This is used to distinguish between empty strings returned by os.GetEnv(key) because of undefined
// environment variables and explicitly empty ones
func getEnvMap() map[string]string {
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		envParts := strings.SplitN(env, "=", 2)
		envMap[envParts[0]] = envParts[1]
	}
	return envMap
}
