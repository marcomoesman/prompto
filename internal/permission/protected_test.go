package permission

import "testing"

func TestProtected_EnvFiles(t *testing.T) {
	cases := []string{
		"/home/user/project/.env",
		"/tmp/app/.env.production",
		"/srv/.env.development.local",
	}
	for _, path := range cases {
		if !IsProtected(path) {
			t.Errorf("%s should be protected", path)
		}
	}
}

func TestProtected_SSHKeys(t *testing.T) {
	cases := []string{
		"/home/user/.ssh/id_rsa",
		"/home/user/.ssh/id_ed25519",
		"/home/user/.ssh/known_hosts",
		"/home/user/.ssh/config",
	}
	for _, path := range cases {
		if !IsProtected(path) {
			t.Errorf("%s should be protected", path)
		}
	}
}

func TestProtected_PemFiles(t *testing.T) {
	cases := []string{
		"/etc/ssl/cert.pem",
		"/home/user/project/server.pem",
	}
	for _, path := range cases {
		if !IsProtected(path) {
			t.Errorf("%s should be protected", path)
		}
	}
}

func TestProtected_GitInternals(t *testing.T) {
	cases := []string{
		"/project/.git/config",
		"/project/.git/hooks/pre-commit",
	}
	for _, path := range cases {
		if !IsProtected(path) {
			t.Errorf("%s should be protected", path)
		}
	}
}

func TestProtected_AWSCredentials(t *testing.T) {
	if !IsProtected("/home/user/.aws/credentials") {
		t.Error(".aws/credentials should be protected")
	}
}

func TestProtected_NormalFilesPass(t *testing.T) {
	cases := []string{
		"/project/src/main.go",
		"/project/README.md",
		"/project/.gitignore",
		"/project/go.mod",
		"/project/.github/workflows/ci.yml",
	}
	for _, path := range cases {
		if IsProtected(path) {
			t.Errorf("%s should NOT be protected", path)
		}
	}
}
