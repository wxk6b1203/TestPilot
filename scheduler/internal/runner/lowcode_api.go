package runner

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/model"
)

// 低代码按接口 ID 调用与自动封装（docs/lowcode-api-invocation.md）。

const maxProjectWrapperAPIs = 200 // 项目全量兜底生成上限；超限要求显式声明 refs

var (
	reCtxHTTPAPI      = regexp.MustCompile(`ctx\.http_api\(\s*["'](\d+)["']`)
	reCtxGRPCAPI      = regexp.MustCompile(`ctx\.grpc_api\(\s*["'](\d+)["']`)
	reCtxAnyAPI       = regexp.MustCompile(`ctx\.api\(\s*["'](\d+)["']`)
	reHTTPCtor        = regexp.MustCompile(`\bHttpAPI\(\s*api_id\s*=\s*["'](\d+)["']`)
	reGRPCCtor        = regexp.MustCompile(`\bGrpcAPI\(\s*api_id\s*=\s*["'](\d+)["']`)
	reWrappersUse     = regexp.MustCompile(`(?m)^\s*(?:from\s+tp_api_wrappers\s+import|import\s+tp_api_wrappers)\b`)
	reWrapperClass    = regexp.MustCompile(`\bApi(\d+)\b`)
	// 常量形式：ECHO_API = "347..." 后再 ctx.http_api(ECHO_API)。
	// 这是 LLM/用户重构脚本时最常见的写法，派发期同样按静态引用解析。
	reAPIConst          = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z_][A-Za-z0-9_]*)[ \t]*=[ \t]*["'](\d+)["'][ \t]*(?:#.*)?$`)
	reCtxHTTPAPIVar     = regexp.MustCompile(`ctx\.http_api\(\s*(?:api_id\s*=\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
	reCtxGRPCAPIVar     = regexp.MustCompile(`ctx\.grpc_api\(\s*(?:api_id\s*=\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
	reCtxAnyAPIVar      = regexp.MustCompile(`ctx\.api\(\s*(?:api_id\s*=\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
	reHTTPCtorVar       = regexp.MustCompile(`\bHttpAPI\(\s*api_id\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\s*`)
	reGRPCCtorVar       = regexp.MustCompile(`\bGrpcAPI\(\s*api_id\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\s*`)
)

type lowCodeRefs struct {
	HTTP        []string
	GRPC        []string
	Any         []string
	FallbackAll bool
}

type lowCodeAPIPrep struct {
	HTTPApis  map[string]*commonv1.HttpApi
	GrpcApis  map[string]*commonv1.GrpcApi
	HTTPNames map[string]string
	GrpcNames map[string]string
}

func newLowCodeAPIPrep() *lowCodeAPIPrep {
	return &lowCodeAPIPrep{
		HTTPApis:  map[string]*commonv1.HttpApi{},
		GrpcApis:  map[string]*commonv1.GrpcApi{},
		HTTPNames: map[string]string{},
		GrpcNames: map[string]string{},
	}
}

func normalizeRefIDs(raw []string) ([]string, error) {
	byNum := map[int64]string{}
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid api ref %q: must be a positive integer id", s)
		}
		byNum[n] = strconv.FormatInt(n, 10)
	}
	nums := make([]int, 0, len(byNum))
	for n := range byNum {
		nums = append(nums, int(n))
	}
	sort.Ints(nums)
	out := make([]string, 0, len(nums))
	for _, n := range nums {
		out = append(out, byNum[int64(n)])
	}
	return out, nil
}

func scanLowCodeAPIRefs(source string) (httpIDs, grpcIDs, anyIDs []string, usesWrappers bool) {
	usesWrappers = reWrappersUse.MatchString(source)
	httpSet := map[string]bool{}
	grpcSet := map[string]bool{}
	anySet := map[string]bool{}
	consts := map[string]string{}
	for _, m := range reAPIConst.FindAllStringSubmatch(source, -1) {
		if len(m) == 3 && m[2] != "" {
			consts[m[1]] = m[2]
		}
	}
	add := func(set map[string]bool, matches [][]string) {
		for _, m := range matches {
			set[m[1]] = true
		}
	}
	addVar := func(set map[string]bool, matches [][]string) {
		for _, m := range matches {
			if id := consts[m[1]]; id != "" {
				set[id] = true
			}
		}
	}
	add(httpSet, reCtxHTTPAPI.FindAllStringSubmatch(source, -1))
	add(grpcSet, reCtxGRPCAPI.FindAllStringSubmatch(source, -1))
	add(anySet, reCtxAnyAPI.FindAllStringSubmatch(source, -1))
	add(httpSet, reHTTPCtor.FindAllStringSubmatch(source, -1))
	add(grpcSet, reGRPCCtor.FindAllStringSubmatch(source, -1))
	addVar(httpSet, reCtxHTTPAPIVar.FindAllStringSubmatch(source, -1))
	addVar(grpcSet, reCtxGRPCAPIVar.FindAllStringSubmatch(source, -1))
	addVar(anySet, reCtxAnyAPIVar.FindAllStringSubmatch(source, -1))
	addVar(httpSet, reHTTPCtorVar.FindAllStringSubmatch(source, -1))
	addVar(grpcSet, reGRPCCtorVar.FindAllStringSubmatch(source, -1))
	if usesWrappers {
		add(anySet, reWrapperClass.FindAllStringSubmatch(source, -1))
	}
	return setToSlice(httpSet), setToSlice(grpcSet), setToSlice(anySet), usesWrappers
}

func setToSlice(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func collectLowCodeRefs(lc *commonv1.LowCodeCase) (lowCodeRefs, error) {
	explicitHTTP, err := normalizeRefIDs(lc.GetHttpApiRefs())
	if err != nil {
		return lowCodeRefs{}, fmt.Errorf("lowcode http_api_refs: %w", err)
	}
	explicitGRPC, err := normalizeRefIDs(lc.GetGrpcApiRefs())
	if err != nil {
		return lowCodeRefs{}, fmt.Errorf("lowcode grpc_api_refs: %w", err)
	}
	scanHTTP, scanGRPC, scanAny, usesWrappers := scanLowCodeAPIRefs(lc.GetSource())
	merged := lowCodeRefs{HTTP: explicitHTTP, GRPC: explicitGRPC, Any: scanAny}
	merged.HTTP = append(merged.HTTP, scanHTTP...)
	merged.GRPC = append(merged.GRPC, scanGRPC...)
	merged.HTTP, err = normalizeRefIDs(merged.HTTP)
	if err != nil {
		return lowCodeRefs{}, err
	}
	merged.GRPC, err = normalizeRefIDs(merged.GRPC)
	if err != nil {
		return lowCodeRefs{}, err
	}
	merged.Any, err = normalizeRefIDs(merged.Any)
	if err != nil {
		return lowCodeRefs{}, err
	}
	if len(merged.HTTP) == 0 && len(merged.GRPC) == 0 && len(merged.Any) == 0 && usesWrappers {
		merged.FallbackAll = true
	}
	return merged, nil
}

func refKeys(ids []string) ([]int64, error) {
	out := make([]int64, 0, len(ids))
	for _, s := range ids {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid api ref %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func fmtKeys(keys []int64) []string {
	out := make([]string, 0, len(keys))
	for _, n := range keys {
		out = append(out, strconv.FormatInt(n, 10))
	}
	return out
}

func (r *Runner) resolveLowCodeAPIs(tenantID, projectID int64, refs lowCodeRefs) (*lowCodeAPIPrep, error) {
	prep := newLowCodeAPIPrep()
	if refs.FallbackAll {
		if err := r.loadProjectAPIs(tenantID, projectID, prep); err != nil {
			return nil, err
		}
	} else {
		if err := r.loadHTTPAPIs(tenantID, refs.HTTP, prep); err != nil {
			return nil, err
		}
		if err := r.loadGrpcAPIs(tenantID, refs.GRPC, prep); err != nil {
			return nil, err
		}
		if err := r.loadAnyAPIs(tenantID, refs.Any, prep); err != nil {
			return nil, err
		}
	}
	for id := range prep.HTTPApis {
		if _, dup := prep.GrpcApis[id]; dup {
			return nil, fmt.Errorf("api %s is referenced as both http and grpc; ambiguous", id)
		}
	}
	return prep, nil
}

func (r *Runner) loadHTTPAPIs(tenantID int64, ids []string, prep *lowCodeAPIPrep) error {
	if len(ids) == 0 {
		return nil
	}
	keys, err := refKeys(ids)
	if err != nil {
		return err
	}
	var rows []model.HttpApi
	if err := r.db.Where("tenant_id = ? AND id IN ?", tenantID, keys).Order("id asc").Find(&rows).Error; err != nil {
		return err
	}
	byID := make(map[int64]model.HttpApi, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	names, err := r.httpTreeNames(tenantID, keys)
	if err != nil {
		return err
	}
	for _, key := range keys {
		row, ok := byID[key]
		if ok == false {
			return fmt.Errorf("http api %d not found in tenant", key)
		}
		proto := ToProtoHTTP(&row)
		prep.HTTPApis[strconv.FormatInt(key, 10)] = proto
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = names[key]
		}
		if name == "" {
			name = fmt.Sprintf("%s %s", httpMethodNameForWrapper(proto.GetMethod()), proto.GetUri())
		}
		prep.HTTPNames[strconv.FormatInt(key, 10)] = name
	}
	return nil
}

func (r *Runner) loadGrpcAPIs(tenantID int64, ids []string, prep *lowCodeAPIPrep) error {
	if len(ids) == 0 {
		return nil
	}
	keys, err := refKeys(ids)
	if err != nil {
		return err
	}
	var rows []model.GrpcApi
	if err := r.db.Where("tenant_id = ? AND id IN ?", tenantID, keys).Order("id asc").Find(&rows).Error; err != nil {
		return err
	}
	byID := make(map[int64]model.GrpcApi, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	names, err := r.grpcTreeNames(tenantID, keys)
	if err != nil {
		return err
	}
	for _, key := range keys {
		row, ok := byID[key]
		if ok == false {
			return fmt.Errorf("grpc api %d not found in tenant", key)
		}
		proto := ToProtoGrpc(&row)
		prep.GrpcApis[strconv.FormatInt(key, 10)] = proto
		name := names[key]
		if name == "" {
			name = fmt.Sprintf("%s/%s", row.FullService, row.Method)
		}
		prep.GrpcNames[strconv.FormatInt(key, 10)] = name
	}
	return nil
}

func (r *Runner) loadAnyAPIs(tenantID int64, ids []string, prep *lowCodeAPIPrep) error {
	if len(ids) == 0 {
		return nil
	}
	keys, err := refKeys(ids)
	if err != nil {
		return err
	}
	var httpRows []model.HttpApi
	if err := r.db.Where("tenant_id = ? AND id IN ?", tenantID, keys).Order("id asc").Find(&httpRows).Error; err != nil {
		return err
	}
	var grpcRows []model.GrpcApi
	if err := r.db.Where("tenant_id = ? AND id IN ?", tenantID, keys).Order("id asc").Find(&grpcRows).Error; err != nil {
		return err
	}
	httpByID := make(map[int64]model.HttpApi, len(httpRows))
	for _, row := range httpRows {
		httpByID[row.ID] = row
	}
	grpcByID := make(map[int64]model.GrpcApi, len(grpcRows))
	for _, row := range grpcRows {
		grpcByID[row.ID] = row
	}
	httpKeys, grpcKeys := []int64{}, []int64{}
	for _, key := range keys {
		_, isHTTP := httpByID[key]
		_, isGRPC := grpcByID[key]
		if isHTTP && isGRPC {
			return fmt.Errorf("api %d is referenced as both http and grpc; ambiguous", key)
		}
		if isHTTP {
			httpKeys = append(httpKeys, key)
		} else if isGRPC {
			grpcKeys = append(grpcKeys, key)
		} else {
			return fmt.Errorf("api %d not found in tenant (neither http nor grpc)", key)
		}
	}
	if err := r.loadHTTPAPIs(tenantID, fmtKeys(httpKeys), prep); err != nil {
		return err
	}
	return r.loadGrpcAPIs(tenantID, fmtKeys(grpcKeys), prep)
}

func (r *Runner) loadProjectAPIs(tenantID, projectID int64, prep *lowCodeAPIPrep) error {
	var httpRows []model.HttpApi
	if err := r.db.Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("id asc").Find(&httpRows).Error; err != nil {
		return err
	}
	var grpcRows []model.GrpcApi
	if err := r.db.Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("id asc").Find(&grpcRows).Error; err != nil {
		return err
	}
	if len(httpRows)+len(grpcRows) > maxProjectWrapperAPIs {
		return fmt.Errorf("project has %d apis, wrapper auto-include limit is %d; "+
			"declare explicit http_api_refs/grpc_api_refs in the lowcode case",
			len(httpRows)+len(grpcRows), maxProjectWrapperAPIs)
	}
	httpKeys := make([]int64, 0, len(httpRows))
	for _, row := range httpRows {
		httpKeys = append(httpKeys, row.ID)
	}
	grpcKeys := make([]int64, 0, len(grpcRows))
	for _, row := range grpcRows {
		grpcKeys = append(grpcKeys, row.ID)
	}
	if err := r.loadHTTPAPIs(tenantID, fmtKeys(httpKeys), prep); err != nil {
		return err
	}
	return r.loadGrpcAPIs(tenantID, fmtKeys(grpcKeys), prep)
}

func (r *Runner) httpTreeNames(tenantID int64, ids []int64) (map[int64]string, error) {
	return r.treeNames(tenantID, model.NodeTypeHTTPAPI, ids)
}

func (r *Runner) grpcTreeNames(tenantID int64, ids []int64) (map[int64]string, error) {
	return r.treeNames(tenantID, model.NodeTypeGRPCAPI, ids)
}

func (r *Runner) treeNames(tenantID int64, nodeType int16, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var nodes []model.TreeNode
	if err := r.db.Where("tenant_id = ? AND node_type = ? AND ref_id IN ?",
		tenantID, nodeType, ids).Order("id asc").Find(&nodes).Error; err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if strings.TrimSpace(n.Name) != "" {
			out[n.RefID] = n.Name
		}
	}
	return out, nil
}

// ---- tp_api_wrappers.py 生成 ----

type apiWrapperSpec struct {
	ID            string
	Kind          string // "Http" | "Grpc"
	Class         string // Api<ID>（稳定别名）
	Alias         string // 可读别名（可为空）
	Doc           string
	ClassDefaults []string // 类字段默认值（只作文档/补全；SDK 不把未显式设置字段当 override）
}

var pyKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

func isASCIIIdent(s string) bool {
	if s == "" || pyKeywords[s] {
		return false
	}
	for i, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
		} else if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func readableAlias(name string) string {
	name = strings.TrimSpace(name)
	if isASCIIIdent(name) {
		return name
	}
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	var b strings.Builder
	for _, f := range fields {
		f = strings.TrimLeftFunc(f, func(r rune) bool {
			return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
		})
		if f == "" {
			continue
		}
		for i, r := range f {
			if i == 0 {
				if r >= 'a' && r <= 'z' {
					r = r - 'a' + 'A'
				}
			} else if r >= 'A' && r <= 'Z' {
				r = r - 'A' + 'a'
			}
			b.WriteRune(r)
		}
	}
	alias := b.String()
	if alias == "" {
		return ""
	}
	if alias[0] >= '0' && alias[0] <= '9' {
		alias = "Api" + alias
	}
	if pyKeywords[alias] {
		alias += "_api"
	}
	if !isASCIIIdent(alias) {
		return ""
	}
	return alias
}

func sortedMapKeys[T any](m map[string]T) []string {
	nums := make([]int, 0, len(m))
	for s := range m {
		n, err := strconv.Atoi(s)
		if err != nil {
			n = 0
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	out := make([]string, 0, len(nums))
	for _, n := range nums {
		out = append(out, strconv.Itoa(n))
	}
	return out
}

func httpMethodNameForWrapper(m commonv1.HttpMethod) string {
	switch m {
	case commonv1.HttpMethod_HTTP_METHOD_GET:
		return "GET"
	case commonv1.HttpMethod_HTTP_METHOD_POST:
		return "POST"
	case commonv1.HttpMethod_HTTP_METHOD_PUT:
		return "PUT"
	case commonv1.HttpMethod_HTTP_METHOD_DELETE:
		return "DELETE"
	case commonv1.HttpMethod_HTTP_METHOD_PATCH:
		return "PATCH"
	case commonv1.HttpMethod_HTTP_METHOD_HEAD:
		return "HEAD"
	case commonv1.HttpMethod_HTTP_METHOD_OPTIONS:
		return "OPTIONS"
	default:
		return "GET"
	}
}

func kvMap(list []*commonv1.KeyValue) map[string]string {
	out := map[string]string{}
	for _, kv := range list {
		if kv.GetKey() != "" {
			out[kv.GetKey()] = kv.GetValue()
		}
	}
	return out
}

func cookieMap(list []*commonv1.CookieParam) map[string]string {
	out := map[string]string{}
	for _, c := range list {
		if c.GetName() != "" {
			out[c.GetName()] = c.GetValue()
		}
	}
	return out
}

// pythonLiteral 把常见 JSON/proto 结构转成合法 Python 字面量（True/False/None）。
func pythonLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return strconv.Quote(x)
	case map[string]string:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, strconv.Quote(k)+": "+strconv.Quote(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, strconv.Quote(k)+": "+pythonLiteral(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, pythonLiteral(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return strconv.Quote(fmt.Sprintf("%v", x))
	}
}

func httpWrapperDefaults(id string, api *commonv1.HttpApi) []string {
	return []string{
		"    api_id: str = " + strconv.Quote(id),
		"    method: str = " + strconv.Quote(httpMethodNameForWrapper(api.GetMethod())),
		"    uri: str = " + strconv.Quote(api.GetUri()),
		"    params: dict = " + pythonLiteral(kvMap(api.GetParams())),
		"    headers: dict = " + pythonLiteral(kvMap(api.GetHeaders())),
		"    cookies: dict = " + pythonLiteral(cookieMap(api.GetCookies())),
	}
}

func grpcWrapperDefaults(id string, api *commonv1.GrpcApi) []string {
	request := any(map[string]any{})
	if api.GetRequestMessage() != nil {
		request = api.GetRequestMessage().AsMap()
	}
	return []string{
		"    api_id: str = " + strconv.Quote(id),
		"    full_service: str = " + strconv.Quote(api.GetFullService()),
		"    method: str = " + strconv.Quote(api.GetMethod()),
		"    request: dict = " + pythonLiteral(request),
		"    metadata: dict = " + pythonLiteral(kvMap(api.GetMetadata())),
	}
}

func buildWrapperSpecs(prep *lowCodeAPIPrep) ([]apiWrapperSpec, error) {
	specs := make([]apiWrapperSpec, 0, len(prep.HTTPApis)+len(prep.GrpcApis))
	add := func(id string, kind string, name string) {
		class := "Api" + id
		alias := readableAlias(name)
		if alias == class {
			alias = ""
		}
		var doc string
		var defaults []string
		if kind == "Http" {
			api := prep.HTTPApis[id]
			doc = fmt.Sprintf("%s · %s %s (api_id=%s)", name,
				httpMethodNameForWrapper(api.GetMethod()), api.GetUri(), id)
			defaults = httpWrapperDefaults(id, api)
		} else {
			api := prep.GrpcApis[id]
			doc = fmt.Sprintf("%s · %s/%s (api_id=%s)", name,
				api.GetFullService(), api.GetMethod(), id)
			defaults = grpcWrapperDefaults(id, api)
		}
		specs = append(specs, apiWrapperSpec{
			ID: id, Kind: kind, Class: class, Alias: alias, Doc: doc,
			ClassDefaults: defaults,
		})
	}
	for _, id := range sortedMapKeys(prep.HTTPApis) {
		add(id, "Http", prep.HTTPNames[id])
	}
	for _, id := range sortedMapKeys(prep.GrpcApis) {
		add(id, "Grpc", prep.GrpcNames[id])
	}
	used := map[string]bool{}
	mark := func(name string) { used[strings.ToLower(name)] = true }
	for _, s := range specs {
		mark(s.Class)
	}
	for i := range specs {
		if specs[i].Alias == "" {
			continue
		}
		base := specs[i].Alias
		candidate := base
		for n := 2; used[strings.ToLower(candidate)]; n++ {
			candidate = fmt.Sprintf("%s_%d", base, n)
		}
		specs[i].Alias = candidate
		mark(candidate)
	}
	return specs, nil
}

func GenerateAPIWrappersSource(prep *lowCodeAPIPrep) (string, error) {
	if prep == nil || (len(prep.HTTPApis) == 0 && len(prep.GrpcApis) == 0) {
		return "", nil
	}
	specs, err := buildWrapperSpecs(prep)
	if err != nil {
		return "", err
	}
	var hasHTTP, hasGRPC bool
	for _, s := range specs {
		hasHTTP = hasHTTP || s.Kind == "Http"
		hasGRPC = hasGRPC || s.Kind == "Grpc"
	}
	var b strings.Builder
	b.WriteString("# auto-generated by TestPilot — do not edit\n")
	b.WriteString("# 每次运行派发时按接口目录最新定义生成；接口修改后脚本无需改动。\n")
	switch {
	case hasHTTP && hasGRPC:
		b.WriteString("from testpilot_sdk import GrpcAPI, HttpAPI\n")
	case hasGRPC:
		b.WriteString("from testpilot_sdk import GrpcAPI\n")
	default:
		b.WriteString("from testpilot_sdk import HttpAPI\n")
	}
	for _, s := range specs {
		fmt.Fprintf(&b, "\n\nclass %s(%sAPI):\n", s.Class, s.Kind)
		fmt.Fprintf(&b, "    %s\n", strconv.Quote(s.Doc))
		for _, line := range s.ClassDefaults {
			fmt.Fprintf(&b, "%s\n", line)
		}
		if s.Alias != "" {
			fmt.Fprintf(&b, "\n%s = %s\n", s.Alias, s.Class)
		}
	}
	names := make([]string, 0, len(specs)*2)
	for _, s := range specs {
		names = append(names, strconv.Quote(s.Class))
		if s.Alias != "" {
			names = append(names, strconv.Quote(s.Alias))
		}
	}
	fmt.Fprintf(&b, "\n\n__all__ = [%s]\n", strings.Join(names, ", "))
	return b.String(), nil
}

// GenerateAPIWrappersStub 生成自包含 .pyi 补全 stub：不 import testpilot_sdk，
// 用户下载到本地工程后 Pylance/Pyright 即可提示 Api<ID>.run() 签名与 Response 字段。
// 该文件仅供 IDE，平台执行仍使用派发时生成的 tp_api_wrappers.py。
func GenerateAPIWrappersStub(prep *lowCodeAPIPrep) (string, error) {
	if prep == nil || (len(prep.HTTPApis) == 0 && len(prep.GrpcApis) == 0) {
		return "", nil
	}
	specs, err := buildWrapperSpecs(prep)
	if err != nil {
		return "", err
	}
	var hasHTTP, hasGRPC bool
	for _, s := range specs {
		hasHTTP = hasHTTP || s.Kind == "Http"
		hasGRPC = hasGRPC || s.Kind == "Grpc"
	}
	var b strings.Builder
	b.WriteString("# auto-generated by TestPilot — IDE completion stub (.pyi, do not execute)\n")
	b.WriteString("# 仅用于本地代码补全；平台执行以派发时生成的 tp_api_wrappers.py 为准。\n")
	b.WriteString("from typing import Any\n\n")
	if hasHTTP {
		b.WriteString("class Response:\n")
		b.WriteString("    status: int = ...\n")
		b.WriteString("    headers: dict[str, str] = ...\n")
		b.WriteString("    body: Any = ...\n")
		b.WriteString("    text: str = ...\n")
		b.WriteString("    elapsed_ms: int = ...\n")
		b.WriteString("    api_id: str | None = ...\n")
		b.WriteString("    request: dict[str, Any] | None = ...\n\n")
	}
	if hasGRPC {
		b.WriteString("class GrpcResponse:\n")
		b.WriteString("    status: str = ...\n")
		b.WriteString("    json: dict[str, Any] = ...\n")
		b.WriteString("    request: dict[str, Any] | None = ...\n")
		b.WriteString("    elapsed_ms: int = ...\n")
		b.WriteString("    api_id: str | None = ...\n\n")
	}
	for _, s := range specs {
		fmt.Fprintf(&b, "class %s:\n", s.Class)
		fmt.Fprintf(&b, "    %s\n", strconv.Quote(s.Doc))
		if s.Kind == "Http" {
			b.WriteString("    api_id: str = ...\n")
			b.WriteString("    method: str = ...\n")
			b.WriteString("    uri: str = ...\n")
			b.WriteString("    params: dict[str, str] = ...\n")
			b.WriteString("    headers: dict[str, str] = ...\n")
			b.WriteString("    cookies: dict[str, str] = ...\n")
			b.WriteString("    def __init__(self, *, api_id: str | None = ..., method: str | None = ..., uri: str | None = ..., headers: dict[str, str] | None = ..., params: dict[str, str] | None = ..., cookies: dict[str, str] | None = ..., body: Any = ..., binary_ref: str | None = ..., timeout: float | None = ...) -> None: ...\n")
			b.WriteString("    async def run(self, *, body: Any = ..., headers: dict[str, str] | None = ..., params: dict[str, str] | None = ..., cookies: dict[str, str] | None = ..., binary_ref: str | None = ..., timeout: float | None = ...) -> Response: ...\n")
		} else {
			b.WriteString("    api_id: str = ...\n")
			b.WriteString("    full_service: str = ...\n")
			b.WriteString("    method: str = ...\n")
			b.WriteString("    request: dict[str, Any] = ...\n")
			b.WriteString("    metadata: dict[str, str] = ...\n")
			b.WriteString("    def __init__(self, *, api_id: str | None = ..., full_service: str | None = ..., method: str | None = ..., request: dict[str, Any] | None = ..., metadata: dict[str, str] | None = ...) -> None: ...\n")
			b.WriteString("    async def run(self, *, request: dict[str, Any] | None = ..., metadata: dict[str, str] | None = ...) -> GrpcResponse: ...\n")
		}
		if s.Alias != "" {
			fmt.Fprintf(&b, "\n%s = %s\n", s.Alias, s.Class)
		}
		b.WriteString("\n\n")
	}
	names := make([]string, 0, len(specs)*2)
	for _, s := range specs {
		names = append(names, strconv.Quote(s.Class))
		if s.Alias != "" {
			names = append(names, strconv.Quote(s.Alias))
		}
	}
	fmt.Fprintf(&b, "__all__ = [%s]\n", strings.Join(names, ", "))
	return b.String(), nil
}

// PreviewAPIWrappers 供 REST 预览端点使用；ids 为空时预览项目全部接口。
func (r *Runner) PreviewAPIWrappers(tenantID, projectID int64, httpIDs, grpcIDs []string) (string, int, error) {
	return r.previewAPIWrappers(tenantID, projectID, httpIDs, grpcIDs, false)
}

// PreviewAPIWrappersStub 同 PreviewAPIWrappers，但返回自包含 .pyi 补全 stub。
func (r *Runner) PreviewAPIWrappersStub(tenantID, projectID int64, httpIDs, grpcIDs []string) (string, int, error) {
	return r.previewAPIWrappers(tenantID, projectID, httpIDs, grpcIDs, true)
}

func (r *Runner) previewAPIWrappers(tenantID, projectID int64, httpIDs, grpcIDs []string, stub bool) (string, int, error) {
	httpNorm, err := normalizeRefIDs(httpIDs)
	if err != nil {
		return "", 0, err
	}
	grpcNorm, err := normalizeRefIDs(grpcIDs)
	if err != nil {
		return "", 0, err
	}
	refs := lowCodeRefs{HTTP: httpNorm, GRPC: grpcNorm}
	if len(httpNorm) == 0 && len(grpcNorm) == 0 {
		refs.FallbackAll = true
	}
	prep, err := r.resolveLowCodeAPIs(tenantID, projectID, refs)
	if err != nil {
		return "", 0, err
	}
	var source string
	if stub {
		source, err = GenerateAPIWrappersStub(prep)
	} else {
		source, err = GenerateAPIWrappersSource(prep)
	}
	if err != nil {
		return "", 0, err
	}
	return source, len(prep.HTTPApis) + len(prep.GrpcApis), nil
}
