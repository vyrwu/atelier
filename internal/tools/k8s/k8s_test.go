package k8s

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"prod":            "prod",
		"arn:aws:eks:foo": "arn_aws_eks_foo",
		"my/context":      "my_context",
		"a-b-c":           "a_b_c",
	}
	for in, want := range cases {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKubeconfigPathFor_UsesXDGCache(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	got, err := kubeconfigPathFor("prod")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "atelier", "k8s", "prod")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCacheKubeconfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	configsFile := filepath.Join(dir, "configs.yaml")
	kc := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\nclusters: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cacheKubeconfig(configsFile, "prod", kc); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configsFile)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]any
	if err := yaml.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	if _, ok := all["prod"]; !ok {
		t.Fatalf("expected key 'prod' in configs.yaml; got: %s", string(data))
	}
	if !strings.Contains(string(data), "apiVersion") {
		t.Fatalf("expected apiVersion in marshalled cache; got: %s", string(data))
	}
}

func TestCacheKubeconfig_PreservesOtherContexts(t *testing.T) {
	dir := t.TempDir()
	configsFile := filepath.Join(dir, "configs.yaml")
	if err := os.WriteFile(configsFile, []byte("staging:\n  apiVersion: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cacheKubeconfig(configsFile, "prod", kc); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configsFile)
	if !strings.Contains(string(data), "staging") {
		t.Fatalf("staging key dropped: %s", string(data))
	}
	if !strings.Contains(string(data), "prod") {
		t.Fatalf("prod key not added: %s", string(data))
	}
}

func TestLoadContexts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "contexts.yaml")
	body := `contexts:
  - name: prod
    context: arn:aws:eks:prod
    authCmd: aws-vault exec prod --
    initCmd: aws eks update-kubeconfig --name prod
  - name: staging
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctxs, err := LoadContexts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctxs) != 2 {
		t.Fatalf("want 2 contexts, got %d", len(ctxs))
	}
	if ctxs[0].Name != "prod" || ctxs[0].KubeContext != "arn:aws:eks:prod" {
		t.Errorf("prod mismatch: %+v", ctxs[0])
	}
	if ctxs[1].KubeContext != "" {
		t.Errorf("staging KubeContext should be empty, got %q", ctxs[1].KubeContext)
	}
}
