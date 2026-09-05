package vast

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DotEnvAllowedKeys is the closed set of variables a .env file may set.
// Everything else is ignored: a .env in an untrusted working directory must
// not be able to redirect traffic (HTTPS_PROXY, VASTAI_BASE_URL), swap the
// trust store (SSL_CERT_FILE), or otherwise influence the process.
var DotEnvAllowedKeys = map[string]bool{
	"VASTAI_API_KEY": true,
	"VAST_API_KEY":   true,
}

// DotEnvResult reports what LoadDotEnv did.
type DotEnvResult struct {
	Path    string   // file that was read ("" if none)
	Set     []string // keys set
	Skipped []string // keys present in the file but not allowed
	Warning string   // non-fatal warning (e.g. permissions)
}

// LoadDotEnv reads KEY=VALUE lines from the first existing file among paths
// (default: ./.env) and sets allowed variables that are not already present
// in the environment. Missing files are not an error.
func LoadDotEnv(paths ...string) (DotEnvResult, error) {
	var res DotEnvResult
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	for _, p := range paths {
		f, err := os.Open(p) // #nosec G304 -- caller-supplied path, only allowlisted keys are read
		if err != nil {
			continue
		}
		res.Path = p
		if fi, err := f.Stat(); err == nil && fi.Mode().Perm()&0o077 != 0 {
			res.Warning = fmt.Sprintf("%s is readable by group/other (mode %o); consider chmod 600", p, fi.Mode().Perm())
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
				v = v[1 : len(v)-1]
			}
			if k == "" {
				continue
			}
			if !DotEnvAllowedKeys[k] {
				res.Skipped = append(res.Skipped, k)
				continue
			}
			if _, exists := os.LookupEnv(k); exists {
				continue
			}
			if err := os.Setenv(k, v); err != nil {
				_ = f.Close()
				return res, fmt.Errorf("set %s from %s: %w", k, p, err)
			}
			res.Set = append(res.Set, k)
		}
		scanErr := sc.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return res, fmt.Errorf("read %s: %w", p, scanErr)
		}
		if closeErr != nil {
			return res, closeErr
		}
		return res, nil
	}
	return res, nil
}
