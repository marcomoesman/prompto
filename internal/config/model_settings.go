package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/marcomoesman/prompto/internal/privatefs"
)

func SetModelSampling(cfg *Config, providerName, modelName string, sampling ModelSampling) error {
	if cfg == nil {
		return errors.New("config is unavailable")
	}
	if sampling.TemperatureConfigured && (sampling.Temperature < 0 || sampling.Temperature > 2) {
		return fmt.Errorf("temperature must be between 0.0 and 2.0")
	}
	if sampling.PresencePenaltyConfigured && (sampling.PresencePenalty < -2 || sampling.PresencePenalty > 2) {
		return fmt.Errorf("presence_penalty must be between -2.0 and 2.0")
	}
	if !setModelSamplingInConfig(cfg, providerName, modelName, sampling) {
		return fmt.Errorf("model %q not found under provider %q", modelName, providerName)
	}
	if err := setModelSamplingInFile(ProjectConfigPath(), providerName, modelName, sampling); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errModelNotInFile) {
		return err
	}
	if err := setModelSamplingInFile(GlobalConfigPath(), providerName, modelName, sampling); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errModelNotInFile) {
		return err
	}
	return fmt.Errorf("model %q under provider %q was not found in project or global config file", modelName, providerName)
}

func ResetModelSampling(cfg *Config, providerName, modelName string) error {
	if cfg == nil {
		return errors.New("config is unavailable")
	}
	if !resetModelSamplingInConfig(cfg, providerName, modelName) {
		return fmt.Errorf("model %q not found under provider %q", modelName, providerName)
	}
	if err := resetModelSamplingInFile(ProjectConfigPath(), providerName, modelName); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errModelNotInFile) {
		return err
	}
	if err := resetModelSamplingInFile(GlobalConfigPath(), providerName, modelName); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errModelNotInFile) {
		return err
	}
	return fmt.Errorf("model %q under provider %q was not found in project or global config file", modelName, providerName)
}

var errModelNotInFile = errors.New("model not in config file")

func setModelSamplingInFile(path, providerName, modelName string, sampling ModelSampling) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	if !setModelSamplingInConfig(&cfg, providerName, modelName, sampling) {
		return errModelNotInFile
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	out = append(out, '\n')
	if err := privatefs.WriteFile(path, out); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func setModelSamplingInConfig(cfg *Config, providerName, modelName string, sampling ModelSampling) bool {
	prov, ok := cfg.Providers[providerName]
	if !ok {
		return false
	}
	for i := range prov.Models {
		if prov.Models[i].Name != modelName {
			continue
		}
		prov.Models[i].Temperature = sampling.TemperaturePtr()
		prov.Models[i].PresencePenalty = sampling.PresencePenaltyPtr()
		cfg.Providers[providerName] = prov
		return true
	}
	return false
}

func resetModelSamplingInFile(path, providerName, modelName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	if !resetModelSamplingInConfig(&cfg, providerName, modelName) {
		return errModelNotInFile
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	out = append(out, '\n')
	if err := privatefs.WriteFile(path, out); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func resetModelSamplingInConfig(cfg *Config, providerName, modelName string) bool {
	prov, ok := cfg.Providers[providerName]
	if !ok {
		return false
	}
	for i := range prov.Models {
		if prov.Models[i].Name != modelName {
			continue
		}
		prov.Models[i].Temperature = nil
		prov.Models[i].PresencePenalty = nil
		cfg.Providers[providerName] = prov
		return true
	}
	return false
}
