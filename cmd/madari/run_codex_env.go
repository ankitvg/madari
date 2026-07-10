package main

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

var unixCodexBaselineKeys = []string{
	"PATH", "SHELL", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TZ", "__CF_USER_TEXT_ENCODING",
}

var windowsCodexBaselineKeys = []string{
	"PATH", "PATHEXT", "COMSPEC", "SYSTEMROOT", "SYSTEMDRIVE", "USERNAME", "USERDOMAIN",
	"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "PROGRAMDATA", "POWERSHELL", "PWSH",
}

func codexPlatformBaseline() map[string]string {
	keys := unixCodexBaselineKeys
	if runtime.GOOS == "windows" {
		keys = windowsCodexBaselineKeys
	}
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return out
}

func captureDeclaredRunEnvironment(env []runPlanEnv) map[string]string {
	out := map[string]string{}
	for _, requirement := range env {
		if !requirement.Present {
			continue
		}
		if codexGeneratedEnvKey(requirement.Key) {
			continue
		}
		if value, ok := os.LookupEnv(requirement.Key); ok {
			out[codexEnvironmentKey(requirement.Key)] = value
		}
	}
	return out
}

func codexGeneratedEnvKey(key string) bool {
	key = codexEnvironmentKey(key)
	switch key {
	case "HOME", "USERPROFILE", "CODEX_HOME", "TMPDIR", "TEMP", "TMP":
		return true
	}
	return runtime.GOOS == "windows" && (key == "APPDATA" || key == "LOCALAPPDATA")
}

func codexEnvironmentKey(key string) string {
	key = strings.TrimSpace(key)
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func cloneEnvMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func envMapList(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
