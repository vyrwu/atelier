package eks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyrwu/atelier/internal/manifest"
)

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"prod":           "prod",
		"prod/us-east-1": "prod_us_east_1",
		"admin@acme.aws": "admin_acme_aws",
		"a b":            "a_b",
	}
	for in, want := range cases {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKubeconfigPathFor_UsesXDGCacheAndEksDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdgcache")
	got, err := kubeconfigPathFor("prod")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/xdgcache", "atelier", "eks", "prod")
	if got != want {
		t.Errorf("kubeconfigPathFor = %q, want %q (under atelier/eks, distinct from k8s)", got, want)
	}
}

func TestCacheKubeconfig_RoundTripAndPreserves(t *testing.T) {
	dir := t.TempDir()
	configsFile := filepath.Join(dir, "configs.yaml")
	kc := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kc, []byte("apiVersion: v1\nclusters: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cacheKubeconfig(configsFile, "prod", kc); err != nil {
		t.Fatalf("cacheKubeconfig prod: %v", err)
	}
	// A second context must not clobber the first.
	if err := os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cacheKubeconfig(configsFile, "stage", kc); err != nil {
		t.Fatalf("cacheKubeconfig stage: %v", err)
	}
	data, _ := os.ReadFile(configsFile)
	s := string(data)
	if !strings.Contains(s, "prod:") || !strings.Contains(s, "stage:") {
		t.Errorf("configs.yaml must retain both contexts, got:\n%s", s)
	}
}

func TestLoadContexts(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "contexts.yaml")
	yaml := `contexts:
  - name: prod-admin
    context: arn:aws:eks:...:cluster/prod
    authCmd: assume prod-admin --exec
    initCmd: aws eks update-kubeconfig --name prod --region eu-west-1 --kubeconfig $KUBECONFIG
  - name: stage
`
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	ctxs, err := LoadContexts(p)
	if err != nil {
		t.Fatalf("LoadContexts: %v", err)
	}
	if len(ctxs) != 2 {
		t.Fatalf("got %d contexts, want 2", len(ctxs))
	}
	if ctxs[0].Name != "prod-admin" || ctxs[0].AuthCmd != "assume prod-admin --exec" || !strings.Contains(ctxs[0].InitCmd, "update-kubeconfig") {
		t.Errorf("context[0] parsed wrong: %+v", ctxs[0])
	}
	// Missing file → helpful error.
	if _, err := LoadContexts(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Error("missing contexts file should error")
	}
}

func TestFindContext(t *testing.T) {
	ctxs := []Context{{Name: "a"}, {Name: "b"}}
	if got := findContext(ctxs, "b"); got == nil || got.Name != "b" {
		t.Errorf("findContext(b) = %+v", got)
	}
	if got := findContext(ctxs, "missing"); got != nil {
		t.Errorf("findContext(missing) = %+v, want nil", got)
	}
}

func TestManifest_ValidGlobalShellTool(t *testing.T) {
	if err := Manifest.Validate(); err != nil {
		t.Fatalf("Manifest.Validate: %v", err)
	}
	if !Manifest.Tool || Manifest.Popup != manifest.KindGlobal {
		t.Errorf("eks must be a KindGlobal Tool, got Tool=%v Popup=%v", Manifest.Tool, Manifest.Popup)
	}
	if Manifest.UI == nil || Manifest.UI.PopupTitle != "EKS" {
		t.Error("selector label must be EKS")
	}
}
