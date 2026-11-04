package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mixedCaseServers replaces the example's empty MCP server list.
const mixedCaseServers = `enabled: true
    servers:
      - name: local
        type: stdio
        command: /bin/true
        env:
          GITHUB_TOKEN: "ghp-test"
          MTest_Secret_Word: "Pineapple"
          lower_case: "x"
          PORT: 8080
      - name: remote
        type: http
        url: "http://localhost:9/mcp"
        headers:
          Authorization: "Bearer t"
          X-Api-Key: "k"
`

// writeExampleWith writes the shipped example config with the MCP server
// list replaced and extra_params blocks added to core.ai and the
// embedding block. It returns the path.
func writeExampleWith(t *testing.T, dir, servers, aiExtra string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "config.yaml.example"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	replace := func(old, new string) {
		t.Helper()
		if strings.Count(s, old) != 1 {
			t.Fatalf("example config: %q not found exactly once", old)
		}
		s = strings.Replace(s, old, new, 1)
	}
	replace("enabled: false\n\n    # All fields below are required ONLY when enabled: true.\n    # Servers can use \"stdio\" (subprocess), \"sse\" (Server-Sent Events),\n    # or \"http\" (Streamable HTTP, MCP 2025-03-26 spec) transport.\n    servers: []\n", servers)
	replace("\n  ai:\n", "\n  ai:\n"+aiExtra)
	replace("\n  embedding:\n", "\n  embedding:\n    extra_params:\n      EncodingFormat: float\n")
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const aiExtraMixed = `    extra_params:
      metadata:
        TraceID: "abc"
        lowercase: "y"
      chat_template_kwargs:
        Enable_Thinking: false
      Stop: ["END"]
`

func checkMixedCase(t *testing.T, cfg *Config) {
	t.Helper()
	if len(cfg.MCP.Servers) != 2 {
		t.Fatalf("servers = %d, want 2", len(cfg.MCP.Servers))
	}
	wantEnv := map[string]string{"GITHUB_TOKEN": "ghp-test", "MTest_Secret_Word": "Pineapple", "lower_case": "x", "PORT": "8080"}
	if got := cfg.MCP.Servers[0].Env; !reflect.DeepEqual(got, wantEnv) {
		t.Errorf("env = %v, want %v", got, wantEnv)
	}
	wantHeaders := map[string]string{"Authorization": "Bearer t", "X-Api-Key": "k"}
	if got := cfg.MCP.Servers[1].Headers; !reflect.DeepEqual(got, wantHeaders) {
		t.Errorf("headers = %v, want %v", got, wantHeaders)
	}
	ep := cfg.AI.ExtraParams
	md, _ := ep["metadata"].(map[string]any)
	if md["TraceID"] != "abc" || md["lowercase"] != "y" || len(md) != 2 {
		t.Errorf("core.ai.extra_params.metadata = %#v", ep["metadata"])
	}
	kw, _ := ep["chat_template_kwargs"].(map[string]any)
	if v, ok := kw["Enable_Thinking"]; !ok || v != false {
		t.Errorf("core.ai.extra_params.chat_template_kwargs = %#v", ep["chat_template_kwargs"])
	}
	if _, ok := ep["Stop"]; !ok {
		t.Errorf("core.ai.extra_params top-level key case lost: %v", ep)
	}
	if cfg.Embedding.ExtraParams["EncodingFormat"] != "float" {
		t.Errorf("memory_pipeline.embedding.extra_params = %v", cfg.Embedding.ExtraParams)
	}
}

// Viper lowercases every map key it reads; env names and API payload keys
// are case-sensitive, so Load must hand them over as written.
func TestLoad_PreservesUserMapKeyCase(t *testing.T) {
	path := writeExampleWith(t, t.TempDir(), mixedCaseServers, aiExtraMixed)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checkMixedCase(t, cfg)
}

// Hot reload re-runs Load on the edited file, and the dashboard writes the
// file with Save first; the case must survive both.
func TestLoad_PreservesUserMapKeyCaseAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := writeExampleWith(t, dir, mixedCaseServers, aiExtraMixed)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Dashboard save, then the watcher's reload.
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checkMixedCase(t, back)
	if !reflect.DeepEqual(cfg.AI.ExtraParams, back.AI.ExtraParams) {
		t.Errorf("save/load changed core.ai.extra_params:\n%#v\n%#v", cfg.AI.ExtraParams, back.AI.ExtraParams)
	}

	// An edit by hand that adds a key and changes the case of another.
	edited := strings.Replace(mixedCaseServers, "lower_case: \"x\"", "Lower_Case: \"x\"\n          NEW_Var: \"n\"", 1)
	path = writeExampleWith(t, dir, edited, aiExtraMixed)
	next, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"GITHUB_TOKEN": "ghp-test", "MTest_Secret_Word": "Pineapple", "Lower_Case": "x", "NEW_Var": "n", "PORT": "8080"}
	if got := next.MCP.Servers[0].Env; !reflect.DeepEqual(got, want) {
		t.Errorf("reloaded env = %v, want %v", got, want)
	}
}

// EZYAPPER_* overrides still apply to map entries; only the key spelling
// comes from the file.
func TestLoad_EnvOverrideKeepsKeyCase(t *testing.T) {
	path := writeExampleWith(t, t.TempDir(), mixedCaseServers, aiExtraMixed)
	t.Setenv("EZYAPPER_CORE_AI_EXTRA_PARAMS_METADATA_TRACEID", "from-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	md, _ := cfg.AI.ExtraParams["metadata"].(map[string]any)
	if md["TraceID"] != "from-env" {
		t.Errorf("metadata = %#v, want TraceID overridden from the environment", md)
	}
}

// Keys that differ only in case are one key to viper (which keeps one value
// at random); both are kept as written.
func TestLoad_KeysDifferingOnlyInCase(t *testing.T) {
	servers := `enabled: true
    servers:
      - name: local
        type: stdio
        command: /bin/true
        env:
          Path: "a"
          PATH: "b"
`
	path := writeExampleWith(t, t.TempDir(), servers, "")
	for range 5 {
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"Path": "a", "PATH": "b"}
		if got := cfg.MCP.Servers[0].Env; !reflect.DeepEqual(got, want) {
			t.Fatalf("env = %v, want %v", got, want)
		}
	}
}

func TestRestoreKeyCase_StructKeysMatchIgnoringCase(t *testing.T) {
	type inner struct {
		Env map[string]string `mapstructure:"env"`
	}
	var got struct {
		Servers []inner `mapstructure:"servers"`
	}
	got.Servers = []inner{{Env: map[string]string{"abc": "1"}}}
	raw := map[string]any{"Servers": []any{map[string]any{"ENV": map[string]any{"AbC": 1}}}}
	restoreKeyCase(reflect.ValueOf(&got).Elem(), raw)
	if !reflect.DeepEqual(got.Servers[0].Env, map[string]string{"AbC": "1"}) {
		t.Fatalf("env = %v", got.Servers[0].Env)
	}
}
