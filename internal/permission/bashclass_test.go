package permission

import "testing"

func TestClassifyBash_ReadOnly(t *testing.T) {
	cases := []string{
		"cat foo",
		"git status",
		"git diff HEAD~1",
		"git log --oneline",
		"git show HEAD",
		"git blame README.md",
		"git ls-files",
		"git branch -l",
		"git branch --list",
		"git tag -l",
		"git remote -v",
		"git config --get user.name",
		"git config --list",
		"git stash list",
		"ls -la",
		"ls",
		"grep -r foo .",
		"rg --files",
		"find . -name '*.go'",
		"pwd",
		`printf %s\n hi`,
		`awk '{print}' file`,
		`sed 's/x/y/' file`,
		"head -n 5 foo",
		"tail -f log",
		"wc -l foo",
		"stat foo",
		"file foo",
		"which go",
		"env",
		"date",
		"uname -a",
		"id",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if got := ClassifyBash(c); got != BashClassReadOnly {
				t.Errorf("ClassifyBash(%q) = %v, want ReadOnly", c, got)
			}
		})
	}
}

func TestClassifyBash_Mutating(t *testing.T) {
	cases := []string{
		"rm -rf foo",
		"mv a b",
		"cp a b",
		"chmod 755 file",
		"chown me file",
		"mkdir new",
		"rmdir old",
		"touch file",
		"ln -s a b",
		"dd if=/dev/zero of=foo bs=1M",
		"truncate -s 0 file",
		"git push",
		"git push origin main",
		"git commit -m msg",
		"git reset --hard",
		"git checkout main",
		"git rebase",
		"git merge feature",
		"git cherry-pick abc",
		"git add -A",
		"git rm file",
		"git mv a b",
		"git init",
		"git clone https://example.com/repo",
		"git fetch",
		"git pull",
		"git apply patch",
		"git am patch",
		"git revert HEAD",
		"git tag v1",
		"git branch new-branch",
		"git stash",
		"git stash push",
		"npm install",
		"npm install --save-dev foo",
		"pnpm add foo",
		"yarn upgrade",
		"bun remove foo",
		"pip install requests",
		"pip3 uninstall x",
		"go install ./cmd/foo",
		"go build ./...",
		"go run main.go",
		"go generate ./...",
		"go mod tidy",
		"go mod download",
		"cargo install ripgrep",
		"cargo build",
		"cargo run",
		"cargo update",
		"sed -i s/x/y/ foo",
		"sed --in-place s/x/y/ foo",
		"sed -i.bak s/x/y/ foo",
		"gawk -i inplace '{print}' file",
		"echo hi > out.txt",
		"echo hi >> out.txt",
		"cat foo | tee out",
		"tee out",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if got := ClassifyBash(c); got != BashClassMutating {
				t.Errorf("ClassifyBash(%q) = %v, want Mutating", c, got)
			}
		})
	}
}

func TestClassifyBash_Unknown(t *testing.T) {
	cases := []string{
		"./foo.sh",
		"make",
		"make build",
		"docker run nginx",
		"aws s3 cp a b",
		"git",
		"git unknown-subcommand",
		"go test ./...",
		"go vet",
		"npm",
		"pip",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if got := ClassifyBash(c); got != BashClassUnknown {
				t.Errorf("ClassifyBash(%q) = %v, want Unknown", c, got)
			}
		})
	}
}

func TestClassifyBash_Pipelines(t *testing.T) {
	cases := []struct {
		cmd  string
		want BashClass
	}{
		{"cat foo | grep bar", BashClassReadOnly},
		{"cat foo | tee out", BashClassMutating},
		{"cat foo | ./script.sh", BashClassUnknown},
		{"ls | wc -l", BashClassReadOnly},
		{"grep foo file | rm bar", BashClassMutating},
		{"ls | head -n 5 | sort", BashClassReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := ClassifyBash(tc.cmd); got != tc.want {
				t.Errorf("ClassifyBash(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestClassifyBash_Quoting(t *testing.T) {
	cases := []struct {
		cmd  string
		want BashClass
	}{
		// `>` inside single quotes is content, not a redirection.
		{"echo 'a > b'", BashClassReadOnly},
		// Same in double quotes.
		{`echo "x > y"`, BashClassReadOnly},
		// `>` inside an unquoted word is a redirection — but inside a
		// quoted word it isn't. Token-level (not substring) detection
		// is the whole point.
		{`grep "evil>foo" file`, BashClassReadOnly},
		// Redirection char inside single quotes for an otherwise mutating-looking
		// command stays read-only because echo is read-only and `>` is quoted.
		{"echo 'rm -rf /'", BashClassReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := ClassifyBash(tc.cmd); got != tc.want {
				t.Errorf("ClassifyBash(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestClassifyBash_SequentialOperators(t *testing.T) {
	cases := []struct {
		cmd  string
		want BashClass
	}{
		{"cat foo; rm bar", BashClassMutating},
		{"cat foo && grep bar baz", BashClassReadOnly},
		{"cat foo || ls", BashClassReadOnly},
		{"ls && rm foo", BashClassMutating},
		{"ls; ls; ls", BashClassReadOnly},
		{"ls;", BashClassReadOnly}, // trailing separator drops the empty segment
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := ClassifyBash(tc.cmd); got != tc.want {
				t.Errorf("ClassifyBash(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestClassifyBash_Empty(t *testing.T) {
	cases := []string{"", " ", "\t", "\n  \t"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if got := ClassifyBash(c); got != BashClassUnknown {
				t.Errorf("ClassifyBash(%q) = %v, want Unknown", c, got)
			}
		})
	}
}

func TestClassifyBash_Subshells(t *testing.T) {
	// We don't recurse into command substitution; treating $(rm foo)
	// as Unknown is safer than risking a ReadOnly mis-classification.
	cases := []string{
		"$(rm foo)",
		"echo $(rm foo)",
		"`rm foo`",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			got := ClassifyBash(c)
			if got == BashClassReadOnly {
				t.Errorf("ClassifyBash(%q) = ReadOnly, want Unknown or Mutating (must not silently allow)", c)
			}
		})
	}
}

func TestClassifyBash_RedirectionVariants(t *testing.T) {
	cases := []struct {
		cmd  string
		want BashClass
	}{
		{"echo hi > out", BashClassMutating},
		{"echo hi >> out", BashClassMutating},
		{"cat <> out", BashClassMutating},
		{"echo hi &> out", BashClassMutating},
		{"echo hi >| out", BashClassMutating},
		// Input redirection on a read-only command stays ReadOnly: `<`
		// doesn't write anywhere, and the head is still `wc`.
		{"wc -l < foo.txt", BashClassReadOnly},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if got := ClassifyBash(tc.cmd); got != tc.want {
				t.Errorf("ClassifyBash(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestClassifyBash_StringMethod(t *testing.T) {
	if BashClassReadOnly.String() != "read-only" {
		t.Errorf("ReadOnly.String() = %q", BashClassReadOnly.String())
	}
	if BashClassMutating.String() != "mutating" {
		t.Errorf("Mutating.String() = %q", BashClassMutating.String())
	}
	if BashClassUnknown.String() != "unknown" {
		t.Errorf("Unknown.String() = %q", BashClassUnknown.String())
	}
}
