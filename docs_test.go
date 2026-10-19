package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kapetan-io/dispatch/internal/config"
)

const referenceMarker = "# every setting at its default"

var tomlBlock = regexp.MustCompile("(?s)```toml\n(.*?)```")

// docExamples returns every toml block in the docs, keyed by file and index.
func docExamples(t *testing.T) map[string]string {
	t.Helper()
	files := []string{"README.md"}
	more, err := filepath.Glob("docs/*.md")
	require.NoError(t, err)
	files = append(files, more...)

	examples := map[string]string{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		for i, m := range tomlBlock.FindAllStringSubmatch(string(body), -1) {
			examples[file+"#"+strconv.Itoa(i+1)] = m[1]
		}
	}
	return examples
}

func loadExample(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return config.Load(path)
}

func TestDocConfigExamplesLoad(t *testing.T) {
	examples := docExamples(t)
	require.NotEmpty(t, examples)

	for name, body := range examples {
		t.Run(name, func(t *testing.T) {
			_, err := loadExample(t, body)

			require.NoError(t, err)
		})
	}
}

func TestDocConfigReferenceListsEveryKeyAtItsDefault(t *testing.T) {
	var reference string
	for _, body := range docExamples(t) {
		if strings.HasPrefix(body, referenceMarker) {
			reference = body
		}
	}
	require.NotEmpty(t, reference)

	got, err := loadExample(t, reference)
	require.NoError(t, err)
	want, err := loadExample(t, "")
	require.NoError(t, err)
	// Repos have no default, so the reference shows one repo at its defaults.
	assert.Equal(t, config.Repo{
		Path:                 config.ExpandHome("~/Development/dispatch"),
		OpenPRAs:             "open",
		IgnoreCommentAuthors: []string{},
		ApprovalChecks:       []string{},
	}, got.Repos["dispatch"])
	got.Repos, want.Repos = nil, nil
	assert.Equal(t, want, got)

	var decoded config.Config
	meta, err := toml.Decode(reference, &decoded)
	require.NoError(t, err)
	documented := map[string]bool{}
	for _, key := range meta.Keys() {
		documented[key.String()] = true
	}
	for _, key := range settingKeys(reflect.TypeOf(config.Config{}), "") {
		assert.Contains(t, documented, key)
	}
	body, err := os.ReadFile("docs/config.md")
	require.NoError(t, err)
	for _, key := range settingKeys(reflect.TypeOf(config.Repo{}), "repos.dispatch.") {
		assert.Contains(t, documented, key)
		assert.Contains(t, string(body), "| `"+strings.TrimPrefix(key, "repos.dispatch.")+"` |")
	}
}

// settingKeys lists the dotted toml name of every leaf setting in typ.
func settingKeys(typ reflect.Type, prefix string) []string {
	var keys []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		tag, _, _ := strings.Cut(field.Tag.Get("toml"), ",")
		name := prefix + tag
		if field.Type.Kind() == reflect.Struct {
			keys = append(keys, settingKeys(field.Type, name+".")...)
			continue
		}
		if field.Type.Kind() != reflect.Map {
			keys = append(keys, name)
		}
	}
	return keys
}

func TestContextDefinesTheRepoSettingsTerms(t *testing.T) {
	body, err := os.ReadFile("docs/CONTEXT.md")
	require.NoError(t, err)

	for _, term := range []string{"Repo settings", "Listed author", "Optional check", "PR phase", "Review question"} {
		assert.Contains(t, string(body), "**"+term+"**:")
	}
}

func TestContextDefinesTheRuleTerms(t *testing.T) {
	body, err := os.ReadFile("docs/CONTEXT.md")
	require.NoError(t, err)

	for _, term := range []string{"World", "Fact", "Unknown", "Cycle", "Once key"} {
		assert.Contains(t, string(body), "**"+term+"**:")
	}
}

func TestGuideInstallCommandsMatchTheMarketplace(t *testing.T) {
	var manifest, catalog struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	for file, into := range map[string]any{
		".claude-plugin/plugin.json":      &manifest,
		".claude-plugin/marketplace.json": &catalog,
	} {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, into))
	}

	require.Len(t, catalog.Plugins, 1)
	assert.Equal(t, manifest.Name, catalog.Plugins[0].Name)
	assert.Equal(t, "./", catalog.Plugins[0].Source)

	guide, err := os.ReadFile("docs/guide.md")
	require.NoError(t, err)
	assert.Contains(t, string(guide), "claude plugin install "+catalog.Plugins[0].Name+"@"+catalog.Name)
}
