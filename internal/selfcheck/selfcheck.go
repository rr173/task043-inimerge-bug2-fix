// Package selfcheck runs an end-to-end verification of the INI parse/merge
// service against an in-process HTTP server. It is invoked by the --smoke-test
// flag and exits the process on completion.
//
// The service is stateless (every request carries its full input), so a single
// shared httptest server is used across all scenarios.
package selfcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"task043-inimerge/internal/httpapi"
)

// client wraps the shared httptest server.
type client struct {
	base string
	c    *http.Client
	srv  *httptest.Server
}

func newClient() *client {
	srv := httptest.NewServer(httpapi.New().Handler())
	return &client{base: srv.URL, c: srv.Client(), srv: srv}
}

func (cl *client) close() { cl.srv.Close() }

func (cl *client) post(path string, body any) (int, map[string]any) {
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, cl.base+path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.c.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	return readBody(resp)
}

func (cl *client) postRaw(path string, raw []byte) (int, map[string]any) {
	req, _ := http.NewRequest(http.MethodPost, cl.base+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.c.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	return readBody(resp)
}

func (cl *client) get(path string) (int, map[string]any) {
	resp, err := cl.c.Get(cl.base + path)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	return readBody(resp)
}

func readBody(resp *http.Response) (int, map[string]any) {
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out
}

// eqInt compares a JSON-decoded number to an expected int.
func eqInt(v any, want int) bool {
	f, ok := v.(float64)
	return ok && int(f) == want
}

// eqStr compares a JSON-decoded value to an expected string.
func eqStr(v any, want string) bool {
	s, ok := v.(string)
	return ok && s == want
}

// sectionsOf extracts the "sections" array as a slice of maps.
func sectionsOf(body map[string]any) []map[string]any {
	return arrayOf(body["sections"])
}

// arrayOf extracts a JSON array of objects as a slice of maps.
func arrayOf(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// keysOf extracts the "keys" array from a section map.
func keysOf(sec map[string]any) []map[string]any {
	return arrayOf(sec["keys"])
}

// conflictsOf extracts the "conflicts" array from a body.
func conflictsOf(body map[string]any) []map[string]any {
	return arrayOf(body["conflicts"])
}

// findSection returns the section with the given name, or nil.
func findSection(secs []map[string]any, name string) map[string]any {
	for _, s := range secs {
		if eqStr(s["name"], name) {
			return s
		}
	}
	return nil
}

// findKey returns the key/value map with the given key name within a section.
func findKey(sec map[string]any, key string) map[string]any {
	for _, kv := range keysOf(sec) {
		if eqStr(kv["key"], key) {
			return kv
		}
	}
	return nil
}

// Run exercises the full HTTP API across the specification, returning nil if
// every behavior matches.
func Run() error {
	cl := newClient()
	defer cl.close()

	scenarios := []struct {
		name string
		fn   func(c *client) error
	}{
		{"健康检查", scenarioHealth},
		{"解析基本段与键", scenarioParseBasic},
		{"解析全局段键", scenarioParseGlobal},
		{"引号值保留井号", scenarioQuotedHash},
		{"行内注释剥离(未引号)", scenarioInlineComment},
		{"井号非空白前置为字面量", scenarioHashLiteral},
		{"引号转义序列", scenarioEscapes},
		{"空值", scenarioEmptyValue},
		{"整行注释与空行忽略", scenarioCommentsIgnored},
		{"段头带行内注释", scenarioSectionWithComment},
		{"重复键 422", scenarioDuplicateKey},
		{"未终止引号 422", scenarioUnterminatedQuote},
		{"非法转义 422", scenarioInvalidEscape},
		{"缺等号 422", scenarioMissingEquals},
		{"空键 422", scenarioEmptyKey},
		{"重复段 422", scenarioDuplicateSection},
		{"空段名 422", scenarioEmptySectionName},
		{"引号后多余内容 422", scenarioTrailingAfterQuote},
		{"合并 last-wins 覆盖记录", scenarioMergeLastWins},
		{"合并非冲突键合并", scenarioMergeNoConflict},
		{"合并不同段独立", scenarioMergeDifferentSections},
		{"合并 fail-on-conflict 409", scenarioMergeFailOnConflict},
		{"合并相同值不冲突", scenarioMergeSameValue},
		{"合并全局段跨文件", scenarioMergeGlobal},
		{"合并缺策略 400", scenarioMergeMissingStrategy},
		{"合并空 configs 400", scenarioMergeEmptyConfigs},
		{"合并配置名缺失 400", scenarioMergeMissingName},
		{"合并中解析失败 422", scenarioMergeParseError},
		{"请求体非合法 JSON 400", scenarioBadJSON},
	}
	for _, sc := range scenarios {
		if err := sc.fn(cl); err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
	}
	return nil
}

func scenarioHealth(c *client) error {
	code, body := c.get("/healthz")
	if code != http.StatusOK || !eqStr(body["status"], "ok") {
		return fmt.Errorf("healthz: code=%d body=%v", code, body)
	}
	return nil
}

func scenarioParseBasic(c *client) error {
	content := "[db]\nhost = localhost\nport = 5432\n"
	code, body := c.post("/parse", map[string]any{"content": content})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	secs := sectionsOf(body)
	if len(secs) != 1 || !eqStr(secs[0]["name"], "db") {
		return fmt.Errorf("sections=%v", secs)
	}
	keys := keysOf(secs[0])
	if len(keys) != 2 {
		return fmt.Errorf("keys len=%d", len(keys))
	}
	if !eqStr(keys[0]["key"], "host") || !eqStr(keys[0]["value"], "localhost") {
		return fmt.Errorf("key0=%v", keys[0])
	}
	if !eqStr(keys[1]["key"], "port") || !eqStr(keys[1]["value"], "5432") {
		return fmt.Errorf("key1=%v", keys[1])
	}
	return nil
}

func scenarioParseGlobal(c *client) error {
	content := "name = app\n[db]\nhost = x\n"
	code, body := c.post("/parse", map[string]any{"content": content})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	secs := sectionsOf(body)
	if len(secs) != 2 {
		return fmt.Errorf("sections len=%d", len(secs))
	}
	if !eqStr(secs[0]["name"], "") {
		return fmt.Errorf("global section name=%v", secs[0]["name"])
	}
	gk := keysOf(secs[0])
	if len(gk) != 1 || !eqStr(gk[0]["key"], "name") || !eqStr(gk[0]["value"], "app") {
		return fmt.Errorf("global keys=%v", gk)
	}
	if !eqStr(secs[1]["name"], "db") {
		return fmt.Errorf("db section name=%v", secs[1]["name"])
	}
	return nil
}

func scenarioQuotedHash(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": `path = "/x # not a comment"` + "\n"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	sec := sectionsOf(body)[0]
	kv := findKey(sec, "path")
	if kv == nil || !eqStr(kv["value"], "/x # not a comment") {
		return fmt.Errorf("value=%v", kv)
	}
	return nil
}

func scenarioInlineComment(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "path = /x # comment\n"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	sec := sectionsOf(body)[0]
	kv := findKey(sec, "path")
	if kv == nil || !eqStr(kv["value"], "/x") {
		return fmt.Errorf("value=%v", kv)
	}
	return nil
}

func scenarioHashLiteral(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "url = http://x#frag\n"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	sec := sectionsOf(body)[0]
	kv := findKey(sec, "url")
	if kv == nil || !eqStr(kv["value"], "http://x#frag") {
		return fmt.Errorf("value=%v", kv)
	}
	return nil
}

func scenarioEscapes(c *client) error {
	// "a\"b\\c\nd"  ->  a"b\c<LF>d
	content := "key = \"a\\\"b\\\\c\\nd\"\n"
	code, body := c.post("/parse", map[string]any{"content": content})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	sec := sectionsOf(body)[0]
	kv := findKey(sec, "key")
	if kv == nil || !eqStr(kv["value"], "a\"b\\c\nd") {
		return fmt.Errorf("value=%q", kv)
	}
	return nil
}

func scenarioEmptyValue(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "key =\n"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	sec := sectionsOf(body)[0]
	kv := findKey(sec, "key")
	if kv == nil || !eqStr(kv["value"], "") {
		return fmt.Errorf("value=%v", kv)
	}
	return nil
}

func scenarioCommentsIgnored(c *client) error {
	content := "# a comment\n\n; another\nkey = v\n"
	code, body := c.post("/parse", map[string]any{"content": content})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	sec := sectionsOf(body)[0]
	if len(keysOf(sec)) != 1 {
		return fmt.Errorf("keys=%v", sec)
	}
	return nil
}

func scenarioSectionWithComment(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "[db] # the db section\nhost = x\n"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	secs := sectionsOf(body)
	if len(secs) != 1 || !eqStr(secs[0]["name"], "db") {
		return fmt.Errorf("sections=%v", secs)
	}
	return nil
}

func scenarioDuplicateKey(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "[s]\nk = 1\nk = 2\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !eqInt(body["line"], 3) {
		return fmt.Errorf("line=%v want 3", body["line"])
	}
	if !strings.Contains(toStr(body["error"]), "duplicate key") {
		return fmt.Errorf("error=%v", body["error"])
	}
	return nil
}

func scenarioUnterminatedQuote(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "key = \"unterminated\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !eqInt(body["line"], 1) {
		return fmt.Errorf("line=%v want 1", body["line"])
	}
	return nil
}

func scenarioInvalidEscape(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": `key = "a\xb"` + "\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !strings.Contains(toStr(body["error"]), "escape") {
		return fmt.Errorf("error=%v", body["error"])
	}
	return nil
}

func scenarioMissingEquals(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "notakeyvalue\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !eqInt(body["line"], 1) {
		return fmt.Errorf("line=%v want 1", body["line"])
	}
	return nil
}

func scenarioEmptyKey(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": " = value\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !strings.Contains(toStr(body["error"]), "empty key") {
		return fmt.Errorf("error=%v", body["error"])
	}
	return nil
}

func scenarioDuplicateSection(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "[a]\nx = 1\n[a]\ny = 2\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !eqInt(body["line"], 3) {
		return fmt.Errorf("line=%v want 3", body["line"])
	}
	if !strings.Contains(toStr(body["error"]), "duplicate section") {
		return fmt.Errorf("error=%v", body["error"])
	}
	return nil
}

func scenarioEmptySectionName(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": "[]\nx = 1\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !strings.Contains(toStr(body["error"]), "empty section name") {
		return fmt.Errorf("error=%v", body["error"])
	}
	return nil
}

func scenarioTrailingAfterQuote(c *client) error {
	code, body := c.post("/parse", map[string]any{"content": `key = "a" b` + "\n"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !strings.Contains(toStr(body["error"]), "unexpected content after quoted value") {
		return fmt.Errorf("error=%v", body["error"])
	}
	return nil
}

func scenarioMergeLastWins(c *client) error {
	configs := []map[string]any{
		{"name": "base.ini", "content": "[s]\nk = 1\nm = 2\n"},
		{"name": "override.ini", "content": "[s]\nk = 9\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	merged, _ := body["merged"].(map[string]any)
	sec := findSection(sectionsOf(merged), "s")
	if sec == nil {
		return fmt.Errorf("missing section s: %v", body)
	}
	if kv := findKey(sec, "k"); kv == nil || !eqStr(kv["value"], "9") {
		return fmt.Errorf("k value=%v want 9", kv)
	}
	if kv := findKey(sec, "m"); kv == nil || !eqStr(kv["value"], "2") {
		return fmt.Errorf("m value=%v want 2", kv)
	}
	conflicts := conflictsOf(body)
	if len(conflicts) != 1 {
		return fmt.Errorf("conflicts len=%d want 1: %v", len(conflicts), conflicts)
	}
	cf := conflicts[0]
	if !eqStr(cf["section"], "s") || !eqStr(cf["key"], "k") ||
		!eqStr(cf["from"], "base.ini") || !eqStr(cf["old_value"], "1") ||
		!eqStr(cf["overridden_by"], "override.ini") || !eqStr(cf["new_value"], "9") {
		return fmt.Errorf("conflict=%v", cf)
	}
	return nil
}

func scenarioMergeNoConflict(c *client) error {
	configs := []map[string]any{
		{"name": "a", "content": "[s]\nx = 1\n"},
		{"name": "b", "content": "[s]\ny = 2\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	merged, _ := body["merged"].(map[string]any)
	sec := findSection(sectionsOf(merged), "s")
	keys := keysOf(sec)
	if len(keys) != 2 {
		return fmt.Errorf("keys len=%d want 2", len(keys))
	}
	if !eqStr(keys[0]["key"], "x") || !eqStr(keys[1]["key"], "y") {
		return fmt.Errorf("order=%v", keys)
	}
	if len(conflictsOf(body)) != 0 {
		return fmt.Errorf("conflicts=%v", conflictsOf(body))
	}
	return nil
}

func scenarioMergeDifferentSections(c *client) error {
	configs := []map[string]any{
		{"name": "a", "content": "[sec1]\nx = 1\n"},
		{"name": "b", "content": "[sec2]\ny = 2\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	merged, _ := body["merged"].(map[string]any)
	secs := sectionsOf(merged)
	if len(secs) != 2 || !eqStr(secs[0]["name"], "sec1") || !eqStr(secs[1]["name"], "sec2") {
		return fmt.Errorf("sections=%v", secs)
	}
	if len(conflictsOf(body)) != 0 {
		return fmt.Errorf("conflicts=%v", conflictsOf(body))
	}
	return nil
}

func scenarioMergeFailOnConflict(c *client) error {
	configs := []map[string]any{
		{"name": "a", "content": "[s]\nk = 1\n"},
		{"name": "b", "content": "[s]\nk = 2\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "fail-on-conflict"})
	if code != http.StatusConflict {
		return fmt.Errorf("code=%d want 409 body=%v", code, body)
	}
	conflicts := conflictsOf(body)
	if len(conflicts) != 1 {
		return fmt.Errorf("conflicts len=%d want 1", len(conflicts))
	}
	cf := conflicts[0]
	if !eqStr(cf["section"], "s") || !eqStr(cf["key"], "k") ||
		!eqStr(cf["old_value"], "1") || !eqStr(cf["new_value"], "2") {
		return fmt.Errorf("conflict=%v", cf)
	}
	if _, hasMerged := body["merged"]; hasMerged {
		return fmt.Errorf("unexpected merged field in 409: %v", body)
	}
	return nil
}

func scenarioMergeSameValue(c *client) error {
	configs := []map[string]any{
		{"name": "a", "content": "[s]\nk = 1\n"},
		{"name": "b", "content": "[s]\nk = 1\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	if len(conflictsOf(body)) != 0 {
		return fmt.Errorf("conflicts=%v want empty", conflictsOf(body))
	}
	merged, _ := body["merged"].(map[string]any)
	sec := findSection(sectionsOf(merged), "s")
	if kv := findKey(sec, "k"); kv == nil || !eqStr(kv["value"], "1") {
		return fmt.Errorf("k value=%v want 1", kv)
	}
	// fail-on-conflict with same value is also not a conflict.
	code2, body2 := c.post("/merge", map[string]any{"configs": configs, "strategy": "fail-on-conflict"})
	if code2 != http.StatusOK {
		return fmt.Errorf("fail-on-conflict same value: code=%d want 200 body=%v", code2, body2)
	}
	return nil
}

func scenarioMergeGlobal(c *client) error {
	configs := []map[string]any{
		{"name": "base", "content": "g = 1\n[s]\nx = 1\n"},
		{"name": "over", "content": "g = 2\n[s]\ny = 2\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusOK {
		return fmt.Errorf("code=%d body=%v", code, body)
	}
	merged, _ := body["merged"].(map[string]any)
	secs := sectionsOf(merged)
	// Global section first (it had keys before any header in the first config).
	if !eqStr(secs[0]["name"], "") {
		return fmt.Errorf("first section=%v want global", secs[0]["name"])
	}
	if kv := findKey(secs[0], "g"); kv == nil || !eqStr(kv["value"], "2") {
		return fmt.Errorf("g value=%v want 2", kv)
	}
	sec := findSection(secs, "s")
	keys := keysOf(sec)
	if len(keys) != 2 {
		return fmt.Errorf("s keys len=%d want 2", len(keys))
	}
	if !eqStr(keys[0]["key"], "x") || !eqStr(keys[1]["key"], "y") {
		return fmt.Errorf("s keys order=%v", keys)
	}
	// The only conflict is the global g override.
	conflicts := conflictsOf(body)
	if len(conflicts) != 1 {
		return fmt.Errorf("conflicts len=%d want 1: %v", len(conflicts), conflicts)
	}
	if !eqStr(conflicts[0]["section"], "") || !eqStr(conflicts[0]["key"], "g") {
		return fmt.Errorf("conflict=%v", conflicts[0])
	}
	return nil
}

func scenarioMergeMissingStrategy(c *client) error {
	configs := []map[string]any{{"name": "a", "content": "[s]\nk = 1\n"}}
	code, body := c.post("/merge", map[string]any{"configs": configs})
	if code != http.StatusBadRequest {
		return fmt.Errorf("code=%d want 400 body=%v", code, body)
	}
	// Invalid strategy value is also 400.
	code2, body2 := c.post("/merge", map[string]any{"configs": configs, "strategy": "weird"})
	if code2 != http.StatusBadRequest {
		return fmt.Errorf("invalid strategy: code=%d want 400 body=%v", code2, body2)
	}
	return nil
}

func scenarioMergeEmptyConfigs(c *client) error {
	code, body := c.post("/merge", map[string]any{"configs": []map[string]any{}, "strategy": "last-wins"})
	if code != http.StatusBadRequest {
		return fmt.Errorf("code=%d want 400 body=%v", code, body)
	}
	return nil
}

func scenarioMergeMissingName(c *client) error {
	configs := []map[string]any{
		{"name": "", "content": "[s]\nk = 1\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusBadRequest {
		return fmt.Errorf("code=%d want 400 body=%v", code, body)
	}
	return nil
}

func scenarioMergeParseError(c *client) error {
	configs := []map[string]any{
		{"name": "good", "content": "[s]\nk = 1\n"},
		{"name": "bad", "content": "[s]\nk = 1\nk = 2\n"},
	}
	code, body := c.post("/merge", map[string]any{"configs": configs, "strategy": "last-wins"})
	if code != http.StatusUnprocessableEntity {
		return fmt.Errorf("code=%d want 422 body=%v", code, body)
	}
	if !eqStr(body["config"], "bad") {
		return fmt.Errorf("config=%v want bad", body["config"])
	}
	if !eqInt(body["line"], 3) {
		return fmt.Errorf("line=%v want 3", body["line"])
	}
	return nil
}

func scenarioBadJSON(c *client) error {
	code, _ := c.postRaw("/parse", []byte("{not json"))
	if code != http.StatusBadRequest {
		return fmt.Errorf("code=%d want 400", code)
	}
	// Unknown field in a well-formed body is also rejected.
	code, _ = c.postRaw("/parse", []byte(`{"content":"x","extra":1}`))
	if code != http.StatusBadRequest {
		return fmt.Errorf("unknown field: code=%d want 400", code)
	}
	return nil
}

// toStr coerces a JSON-decoded value to a string for substring checks.
func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
