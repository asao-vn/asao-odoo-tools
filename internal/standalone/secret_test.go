package standalone_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/cli/internal/secretinput"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
)

// typesOnThePage stands in for the person: when the page opens, it reads it and submits value.
func typesOnThePage(t *testing.T, value string) {
	t.Helper()
	token := regexp.MustCompile(`name="token" value="([0-9a-f]+)"`)
	secretinput.OpenBrowser = func(address string) error {
		go func() {
			response, err := http.Get(address)
			if err != nil {
				t.Error(err)
				return
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			match := token.FindStringSubmatch(string(body))
			if match == nil {
				t.Error("no form token on the page")
				return
			}
			origin := address[:strings.LastIndex(address, "/")]
			request, _ := http.NewRequest(http.MethodPost, address, strings.NewReader(url.Values{"token": {match[1]}, "value": {value}}.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", origin)
			if answer, err := http.DefaultClient.Do(request); err == nil {
				answer.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { secretinput.OpenBrowser = func(string) error { return nil } })
}

func TestCredentialSetOnThePageStoresWhatThePersonTyped(t *testing.T) {
	restore := localprofile.SetDefaultKeyringStore(unavailableKeyringStore{})
	defer restore()
	t.Setenv(localprofile.CredentialFileVariable, filepath.Join(t.TempDir(), "credentials.json"))
	writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab-a","profiles":{
	  "lab-a":{"endpoint":"http://127.0.0.1:8069","database":"lab-a","login":"admin","credentialRef":"lab-a"}}}`)
	const typed = "typed-on-the-page-0123"
	typesOnThePage(t, typed)
	code, out, diag := runWithStdin(t, nil, "credential", "set", "--ref", "lab-a", "--login", "admin", "--browser", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, diag)
	}
	if strings.Contains(out.String(), typed) || strings.Contains(diag.String(), typed) {
		t.Fatalf("the value was printed: %s %s", out, diag)
	}
	if status := decode(t, out)["status"]; status != "STORED" {
		t.Fatalf("status %v", status)
	}
	material, err := localprofile.ResolveCredential(localprofile.Profile{Name: "lab-a", CredentialRef: "lab-a"})
	if err != nil || material.Password != typed {
		t.Fatalf("stored %q, %v", material.Password, err)
	}
}

func TestSecretEnvWritesItsLineAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\nexport ODOO_API_KEY=old\n# a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	typesOnThePage(t, "new key with spaces")
	code, out, diag := runWithStdin(t, nil, "secret", "env", "--file", path, "--name", "ODOO_API_KEY", "--browser", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, diag)
	}
	if strings.Contains(out.String(), "new key") {
		t.Fatalf("the value was printed: %s", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "A=1\nODOO_API_KEY='new key with spaces'\n# a comment\n" {
		t.Fatalf("the file: %q", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	if result := decode(t, out); result["replaced"] != true || result["stored"] != true {
		t.Fatalf("result %v", result)
	}
}

func TestSecretEnvWritesOnlyEnvFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.env")
	_ = os.WriteFile(target, []byte(""), 0o600)
	link := filepath.Join(dir, ".env")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	for _, file := range []string{filepath.Join(dir, "config.yaml"), filepath.Join(dir, ".bashrc"), link} {
		code, out, _ := runWithStdin(t, nil, "secret", "env", "--file", file, "--name", "X", "--browser", "--output", "json")
		if code == clicore.ExitOK || decodeError(t, out)["code"] != "SECRET_FILE_REFUSED" {
			t.Errorf("%s: exit %d %s", file, code, out)
		}
	}
}

func TestAgentsAskForSecretsOnlyThroughThePage(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var out, diag bytes.Buffer
	if code := standalone.ServeMCP(context.Background(), "odoo-cli", []string{"--mode", "tools", "--allow-writes"}, strings.NewReader(input), &out, &diag); code != 0 {
		t.Fatalf("exit %d: %s", code, diag.String())
	}
	tools := map[string]map[string]interface{}{}
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		var reply map[string]interface{}
		_ = json.Unmarshal(scanner.Bytes(), &reply)
		if reply["id"] == 2.0 {
			for _, item := range reply["result"].(map[string]interface{})["tools"].([]interface{}) {
				tool := item.(map[string]interface{})
				tools[tool["name"].(string)] = tool
			}
		}
	}
	for name, hidden := range map[string]string{"credential_set": "password-stdin", "secret_env": "stdin"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("%s is not offered with --allow-writes", name)
		}
		properties := tool["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
		if _, offered := properties[hidden]; offered {
			t.Errorf("%s offers --%s to the agent", name, hidden)
		}
		if _, ok := properties["browser"]; !ok {
			t.Errorf("%s does not offer --browser", name)
		}
	}
}
