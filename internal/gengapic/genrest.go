// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gengapic

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/googleapis/gapic-generator-go/internal/errors"
	"github.com/googleapis/gapic-generator-go/internal/pbinfo"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
)

func lowcaseRestClientName(servName string) string {
	if servName == "" {
		return "restClient"
	}

	return lowerFirst(servName + "RESTClient")
}

func (g *generator) restClientInit(serv *descriptor.ServiceDescriptorProto, servName string, imp pbinfo.ImportSpec, hasRPCForLRO bool) {
	p := g.printf
	lowcaseServName := lowcaseRestClientName(servName)

	p("// Methods, except Close, may be called concurrently. However, fields must not be modified concurrently with method calls.")
	p("type %s struct {", lowcaseServName)
	p("  // The http endpoint to connect to.")
	p("  endpoint string")
	p("")
	p("  // The http client.")
	p("  httpClient *http.Client")
	p("")
	if opServ, ok := g.customOpServices[serv]; ok {
		opServName := pbinfo.ReduceServName(opServ.GetName(), g.opts.pkgName)
		p("// operationClient is used to call the operation-specific management service.")
		p("operationClient *%sClient", opServName)
		p("")
	}
	p("	 // The x-goog-* metadata to be sent with each request.")
	p("	 xGoogMetadata metadata.MD")
	p("}")
	p("")
	g.restClientUtilities(serv, servName, imp, hasRPCForLRO)

	g.imports[pbinfo.ImportSpec{Path: "net/http"}] = true
	g.imports[pbinfo.ImportSpec{Path: "net/url"}] = true
	g.imports[pbinfo.ImportSpec{Path: "io/ioutil"}] = true
	g.imports[pbinfo.ImportSpec{Path: "fmt"}] = true
	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/grpc/metadata"}] = true
	g.imports[pbinfo.ImportSpec{Name: "httptransport", Path: "google.golang.org/api/transport/http"}] = true
	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/api/option/internaloption"}] = true
}

func (g *generator) genRESTMethods(serv *descriptor.ServiceDescriptorProto, servName string) error {
	g.addMetadataServiceForTransport(serv.GetName(), "rest", servName)

	methods := append(serv.GetMethod(), g.getMixinMethods()...)

	for _, m := range methods {
		g.methodDoc(m)
		if err := g.genRESTMethod(servName, serv, m); err != nil {
			return errors.E(err, "method: %s", m.GetName())
		}
		g.addMetadataMethod(serv.GetName(), "rest", m.GetName())
	}

	return nil
}

func (g *generator) restClientOptions(serv *descriptor.ServiceDescriptorProto, servName string) error {
	if !proto.HasExtension(serv.GetOptions(), annotations.E_DefaultHost) {
		// Not an error, just doesn't apply to us.
		return nil
	}

	p := g.printf

	eHost := proto.GetExtension(serv.GetOptions(), annotations.E_DefaultHost)

	// Default to https, just as gRPC defaults to a secure connection.
	host := fmt.Sprintf("https://%s", eHost.(string))

	p("func default%sRESTClientOptions() []option.ClientOption {", servName)
	p("  return []option.ClientOption{")
	p("    internaloption.WithDefaultEndpoint(%q),", host)
	p("    internaloption.WithDefaultMTLSEndpoint(%q),", generateDefaultMTLSEndpoint(host))
	p("    internaloption.WithDefaultAudience(%q),", generateDefaultAudience(host))
	p("    internaloption.WithDefaultScopes(DefaultAuthScopes()...),")
	p("  }")
	p("}")

	return nil
}

func (g *generator) restClientUtilities(serv *descriptor.ServiceDescriptorProto, servName string, imp pbinfo.ImportSpec, hasRPCForLRO bool) {
	p := g.printf
	lowcaseServName := lowcaseRestClientName(servName)
	clientName := camelToSnake(serv.GetName())
	clientName = strings.Replace(clientName, "_", " ", -1)
	opServ, hasCustomOp := g.customOpServices[serv]

	p("// New%sRESTClient creates a new %s rest client.", servName, clientName)
	g.serviceDoc(serv)
	p("func New%[1]sRESTClient(ctx context.Context, opts ...option.ClientOption) (*%[1]sClient, error) {", servName)
	p("    clientOpts := append(default%sRESTClientOptions(), opts...)", servName)
	p("    httpClient, endpoint, err := httptransport.NewClient(ctx, clientOpts...)")
	p("    if err != nil {")
	p("        return nil, err")
	p("    }")
	p("")
	p("    c := &%s{", lowcaseServName)
	p("        endpoint: endpoint,")
	p("        httpClient: httpClient,")
	p("    }")
	p("    c.setGoogleClientInfo()")
	p("")
	if hasCustomOp {
		opServName := pbinfo.ReduceServName(opServ.GetName(), g.opts.pkgName)
		p("o := []option.ClientOption{")
		p("  option.WithHTTPClient(httpClient),")
		p("  option.WithEndpoint(endpoint),")
		p("}")
		p("opC, err := New%sRESTClient(ctx, o...)", opServName)
		p("if err != nil {")
		p("  return nil, err")
		p("}")
		p("c.operationClient = opC")
		p("")
		g.imports[pbinfo.ImportSpec{Path: "google.golang.org/api/option"}] = true
	}
	// TODO(dovs): make rest default call options
	// TODO(dovs): set the LRO client
	p("    return &%[1]sClient{internalClient: c, CallOptions: &%[1]sCallOptions{}}, nil", servName)
	p("}")
	p("")

	g.restClientOptions(serv, servName)

	// setGoogleClientInfo method
	p("// setGoogleClientInfo sets the name and version of the application in")
	p("// the `x-goog-api-client` header passed on each request. Intended for")
	p("// use by Google-written clients.")
	p("func (c *%s) setGoogleClientInfo(keyval ...string) {", lowcaseServName)
	p(`  kv := append([]string{"gl-go", versionGo()}, keyval...)`)
	p(`  kv = append(kv, "gapic", versionClient, "gax", gax.Version, "rest", "UNKNOWN")`)
	p(`  c.xGoogMetadata = metadata.Pairs("x-goog-api-client", gax.XGoogHeader(kv...))`)
	p("}")
	p("")

	// Close method
	p("// Close closes the connection to the API service. The user should invoke this when")
	p("// the client is no longer required.")
	p("func (c *%s) Close() error {", lowcaseServName)
	p("    // Replace httpClient with nil to force cleanup.")
	p("    c.httpClient = nil")
	if hasCustomOp {
		p("if err := c.operationClient.Close(); err != nil {")
		p("  return err")
		p("}")
	}
	p("    return nil")
	p("}")
	p("")

	p("// Connection returns a connection to the API service.")
	p("//")
	p("// Deprecated.")
	p("func (c *%s) Connection() *grpc.ClientConn {", lowcaseServName)
	p("    return nil")
	p("}")
}

type httpInfo struct {
	verb, url, body string
}

func (g *generator) pathParams(m *descriptor.MethodDescriptorProto) map[string]*descriptor.FieldDescriptorProto {
	pathParams := map[string]*descriptor.FieldDescriptorProto{}
	info := getHTTPInfo(m)
	if info == nil {
		return pathParams
	}

	// Match using the curly braces but don't include them in the grouping.
	re := regexp.MustCompile("{([^}]+)}")
	for _, p := range re.FindAllStringSubmatch(info.url, -1) {
		// In the returned slice, the zeroth element is the full regex match,
		// and the subsequent elements are the sub group matches.
		// See the docs for FindStringSubmatch for further details.
		param := p[1]
		field := g.lookupField(m.GetInputType(), param)
		if field == nil {
			continue
		}
		pathParams[param] = field
	}

	return pathParams
}

func (g *generator) queryParams(m *descriptor.MethodDescriptorProto) map[string]*descriptor.FieldDescriptorProto {
	queryParams := map[string]*descriptor.FieldDescriptorProto{}
	info := getHTTPInfo(m)
	if info == nil {
		return queryParams
	}
	if info.body == "*" {
		// The entire request is the REST body.
		return queryParams
	}

	pathParams := g.pathParams(m)
	// Minor hack: we want to make sure that the body parameter is NOT a query parameter.
	pathParams[info.body] = &descriptor.FieldDescriptorProto{}

	request := g.descInfo.Type[m.GetInputType()].(*descriptor.DescriptorProto)
	// Body parameters are fields present in the request body.
	// This may be the request message itself or a subfield.
	// Body parameters are not valid query parameters,
	// because that means the same param would be sent more than once.
	bodyField := g.lookupField(m.GetInputType(), info.body)

	// Possible query parameters are all leaf fields in the request or body.
	pathToLeaf := g.getLeafs(request, bodyField)
	// Iterate in sorted order to
	for path, leaf := range pathToLeaf {
		// If, and only if, a leaf field is not a path parameter or a body parameter,
		// it is a query parameter.
		if _, ok := pathParams[path]; !ok && g.lookupField(request.GetName(), leaf.GetName()) == nil {
			queryParams[path] = leaf
		}
	}

	return queryParams
}

// Returns a map from fully qualified path to field descriptor for all the leaf fields of a message 'm',
// where a "leaf" field is a non-message whose top message ancestor is 'm'.
// e.g. for a message like the following
//
// message Mollusc {
//     message Squid {
//         message Mantle {
//             int32 mass_kg = 1;
//         }
//         Mantle mantle = 1;
//     }
//     Squid squid = 1;
// }
//
// The one entry would be
// "squid.mantle.mass_kg": *descriptor.FieldDescriptorProto...
func (g *generator) getLeafs(msg *descriptor.DescriptorProto, excludedFields ...*descriptor.FieldDescriptorProto) map[string]*descriptor.FieldDescriptorProto {
	pathsToLeafs := map[string]*descriptor.FieldDescriptorProto{}

	contains := func(fields []*descriptor.FieldDescriptorProto, field *descriptor.FieldDescriptorProto) bool {
		for _, f := range fields {
			if field == f {
				return true
			}
		}
		return false
	}

	// We need to declare and define this function in two steps
	// so that we can use it recursively.
	var recurse func([]*descriptor.FieldDescriptorProto, *descriptor.DescriptorProto)

	handleLeaf := func(field *descriptor.FieldDescriptorProto, stack []*descriptor.FieldDescriptorProto) {
		elts := []string{}
		for _, f := range stack {
			elts = append(elts, f.GetName())
		}
		elts = append(elts, field.GetName())
		key := strings.Join(elts, ".")
		pathsToLeafs[key] = field
	}

	handleMsg := func(field *descriptor.FieldDescriptorProto, stack []*descriptor.FieldDescriptorProto) {
		if field.GetLabel() == descriptor.FieldDescriptorProto_LABEL_REPEATED {
			// Repeated message fields must not be mapped because no
			// client library can support such complicated mappings.
			// https://cloud.google.com/endpoints/docs/grpc-service-config/reference/rpc/google.api#grpc-transcoding
			return
		}
		if contains(excludedFields, field) {
			return
		}
		// Short circuit on infinite recursion
		if contains(stack, field) {
			return
		}

		subMsg := g.descInfo.Type[field.GetTypeName()].(*descriptor.DescriptorProto)
		recurse(append(stack, field), subMsg)
	}

	recurse = func(
		stack []*descriptor.FieldDescriptorProto,
		m *descriptor.DescriptorProto,
	) {
		for _, field := range m.GetField() {
			if field.GetType() == descriptor.FieldDescriptorProto_TYPE_MESSAGE {
				handleMsg(field, stack)
			} else {
				handleLeaf(field, stack)
			}
		}
	}

	recurse([]*descriptor.FieldDescriptorProto{}, msg)
	return pathsToLeafs
}

func (g *generator) generateQueryString(m *descriptor.MethodDescriptorProto) {
	p := g.printf
	queryParams := g.queryParams(m)
	if len(queryParams) == 0 {
		return
	}

	// We want to iterate over fields in a deterministic order
	// to prevent spurious deltas when regenerating gapics.
	fields := make([]string, 0, len(queryParams))
	for p := range queryParams {
		fields = append(fields, p)
	}
	sort.Strings(fields)

	p("params := url.Values{}")
	for _, path := range fields {
		field := queryParams[path]
		required := isRequired(field)
		accessor := fieldGetter(path)
		singularPrimitive := field.GetType() != fieldTypeMessage &&
			field.GetType() != fieldTypeBytes &&
			field.GetLabel() != fieldLabelRepeated
		paramAdd := fmt.Sprintf("params.Add(%q, fmt.Sprintf(%q, req%s))", lowerFirst(snakeToCamel(path)), "%v", accessor)

		// Only required, singular, primitive field types should be added regardless.
		if required && singularPrimitive {
			// Use string format specifier here in order to allow %v to be a raw string.
			p("%s", paramAdd)
			continue
		}

		if field.GetLabel() == fieldLabelRepeated {
			// It's a slice, so check for nil
			p("if req%s != nil {", accessor)
		} else if field.GetProto3Optional() {
			// Split right before the raw access
			toks := strings.Split(path, ".")
			toks = toks[:len(toks)-1]
			parentField := fieldGetter(strings.Join(toks, "."))
			directLeafField := directAccess(path)
			p("if req%s != nil && req%s != nil {", parentField, directLeafField)
		} else {
			// Default values are type specific
			switch field.GetType() {
			// Degenerate case, field should never be a message because that implies it's not a leaf.
			case fieldTypeMessage, fieldTypeBytes:
				p("if req%s != nil {", accessor)
			case fieldTypeString:
				p(`if req%s != "" {`, accessor)
			case fieldTypeBool:
				p(`if req%s {`, accessor)
			default: // Handles all numeric types including enums
				p(`if req%s != 0 {`, accessor)
			}
		}
		p("    %s", paramAdd)
		p("}")
	}
	p("")
	p("baseUrl.RawQuery = params.Encode()")
	p("")
}

func (g *generator) generateURLString(m *descriptor.MethodDescriptorProto) error {
	info := getHTTPInfo(m)
	if info == nil {
		return errors.E(nil, "method has no http info: %s", m.GetName())
	}

	p := g.printf

	fmtStr := info.url
	// TODO(dovs): handle more complex path urls involving = and *,
	// e.g. v1beta1/repeat/{info.f_string=first/*}/{info.f_child.f_string=second/**}:pathtrailingresource
	re := regexp.MustCompile(`{([a-zA-Z0-9_.]+?)(=[^{}]+)?}`)
	fmtStr = re.ReplaceAllStringFunc(fmtStr, func(s string) string { return "%v" })

	// TODO(dovs): handle error
	p("baseUrl, _ := url.Parse(c.endpoint)")

	tokens := []string{fmt.Sprintf(`"%s"`, fmtStr)}
	// Can't just reuse pathParams because the order matters
	for _, path := range re.FindAllStringSubmatch(info.url, -1) {
		// In the returned slice, the zeroth element is the full regex match,
		// and the subsequent elements are the sub group matches.
		// See the docs for FindStringSubmatch for further details.
		tokens = append(tokens, fmt.Sprintf("req%s", fieldGetter(path[1])))
	}
	p("baseUrl.Path += fmt.Sprintf(%s)", strings.Join(tokens, ", "))
	p("")
	return nil
}

func getHTTPInfo(m *descriptor.MethodDescriptorProto) *httpInfo {
	if m == nil || m.GetOptions() == nil {
		return nil
	}

	eHTTP := proto.GetExtension(m.GetOptions(), annotations.E_Http)

	httpRule := eHTTP.(*annotations.HttpRule)
	info := httpInfo{body: httpRule.GetBody()}

	switch httpRule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		info.verb = "get"
		info.url = httpRule.GetGet()
	case *annotations.HttpRule_Post:
		info.verb = "post"
		info.url = httpRule.GetPost()
	case *annotations.HttpRule_Patch:
		info.verb = "patch"
		info.url = httpRule.GetPatch()
	case *annotations.HttpRule_Put:
		info.verb = "put"
		info.url = httpRule.GetPut()
	case *annotations.HttpRule_Delete:
		info.verb = "delete"
		info.url = httpRule.GetDelete()
	}

	return &info
}

// genRESTMethod generates a single method from a client. m must be a method declared in serv.
// If the generated method requires an auxiliary type, it is added to aux.
func (g *generator) genRESTMethod(servName string, serv *descriptor.ServiceDescriptorProto, m *descriptor.MethodDescriptorProto) error {
	if g.isLRO(m) {
		g.aux.lros[m] = true
		return g.lroRESTCall(servName, m)
	}

	if m.GetOutputType() == emptyType {
		return g.emptyUnaryRESTCall(servName, m)
	}

	if pf, ps, err := g.getPagingFields(m); err != nil {
		return err
	} else if pf != nil {
		iter, err := g.iterTypeOf(pf)
		if err != nil {
			return err
		}

		return g.pagingRESTCall(servName, m, pf, ps, iter)
	}

	switch {
	case m.GetClientStreaming():
		return g.noRequestStreamRESTCall(servName, serv, m)
	case m.GetServerStreaming():
		return g.serverStreamRESTCall(servName, serv, m)
	default:
		return g.unaryRESTCall(servName, m)
	}
}

func (g *generator) serverStreamRESTCall(servName string, s *descriptor.ServiceDescriptorProto, m *descriptor.MethodDescriptorProto) error {
	// Streaming calls are not currently supported for REST clients,
	// but the interface signature must be preserved.
	// Unimplemented REST methods will always error.

	inType := g.descInfo.Type[m.GetInputType()]

	inSpec, err := g.descInfo.ImportSpec(inType)
	if err != nil {
		return err
	}
	g.imports[inSpec] = true

	servSpec, err := g.descInfo.ImportSpec(s)
	if err != nil {
		return err
	}
	g.imports[servSpec] = true

	p := g.printf
	lowcaseServName := lowcaseRestClientName(servName)
	p("func (c *%s) %s(ctx context.Context, req *%s.%s, opts ...gax.CallOption) (%s.%s_%sClient, error) {",
		lowcaseServName, m.GetName(), inSpec.Name, inType.GetName(), servSpec.Name, s.GetName(), m.GetName())
	p(`  return nil, fmt.Errorf("%s not yet supported for REST clients")`, m.GetName())
	p("}")
	p("")

	return nil
}

func (g *generator) noRequestStreamRESTCall(servName string, s *descriptor.ServiceDescriptorProto, m *descriptor.MethodDescriptorProto) error {
	// Streaming calls are not currently supported for REST clients,
	// but the interface signature must be preserved.
	// Unimplemented REST methods will always error.

	p := g.printf

	servSpec, err := g.descInfo.ImportSpec(s)
	if err != nil {
		return err
	}
	g.imports[servSpec] = true

	lowcaseServName := lowcaseRestClientName(servName)

	p("func (c *%s) %s(ctx context.Context, opts ...gax.CallOption) (%s.%s_%sClient, error) {",
		lowcaseServName, m.GetName(), servSpec.Name, s.GetName(), m.GetName())
	p(`  return nil, fmt.Errorf("%s not yet supported for REST clients")`, m.GetName())
	p("}")
	p("")

	return nil
}

func (g *generator) pagingRESTCall(servName string, m *descriptor.MethodDescriptorProto, elemField, pageSize *descriptor.FieldDescriptorProto, pt *iterType) error {
	lowcaseServName := lowcaseRestClientName(servName)
	p := g.printf

	inType := g.descInfo.Type[m.GetInputType()].(*descriptor.DescriptorProto)
	outType := g.descInfo.Type[m.GetOutputType()].(*descriptor.DescriptorProto)

	inSpec, err := g.descInfo.ImportSpec(inType)
	if err != nil {
		return err
	}

	outSpec, err := g.descInfo.ImportSpec(outType)
	if err != nil {
		return err
	}
	info := getHTTPInfo(m)
	if err != nil {
		return err
	}
	if info == nil {
		return errors.E(nil, "method has no http info: %s", m.GetName())
	}

	verb := strings.ToUpper(info.verb)

	max := "math.MaxInt32"
	g.imports[pbinfo.ImportSpec{Path: "math"}] = true
	psTyp := pbinfo.GoTypeForPrim[pageSize.GetType()]
	ps := fmt.Sprintf("%s(pageSize)", psTyp)
	if isOptional(inType, pageSize.GetName()) {
		max = fmt.Sprintf("proto.%s(%s)", upperFirst(psTyp), max)
		ps = fmt.Sprintf("proto.%s(%s)", upperFirst(psTyp), ps)
	}
	tok := "pageToken"
	if isOptional(inType, "page_token") {
		tok = fmt.Sprintf("proto.String(%s)", tok)
	}

	pageSizeFieldName := snakeToCamel(pageSize.GetName())
	p("func (c *%s) %s(ctx context.Context, req *%s.%s, opts ...gax.CallOption) *%s {",
		lowcaseServName, m.GetName(), inSpec.Name, inType.GetName(), pt.iterTypeName)
	p("it := &%s{}", pt.iterTypeName)
	p("req = proto.Clone(req).(*%s.%s)", inSpec.Name, inType.GetName())

	maybeReqBytes := "nil"
	if info.body != "" {
		p("m := protojson.MarshalOptions{AllowPartial: true, UseProtoNames: false}")
		maybeReqBytes = "bytes.NewReader(jsonReq)"
		g.imports[pbinfo.ImportSpec{Path: "bytes"}] = true
	}

	p("unm := protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true}")
	p("it.InternalFetch = func(pageSize int, pageToken string) ([]%s, string, error) {", pt.elemTypeName)
	g.internalFetchSetup(outType, outSpec, tok, pageSizeFieldName, max, ps)

	if info.body != "" {
		p("  jsonReq, err := m.Marshal(req)")
		p("  if err != nil {")
		p(`    return nil, "", err`)
		p("  }")
		p("")
	}

	g.generateURLString(m)
	g.generateQueryString(m)
	p("  // Build HTTP headers from client and context metadata.")
	p(`  headers := buildHeaders(ctx, c.xGoogMetadata, metadata.Pairs("Content-Type", "application/json"))`)
	p("  e := gax.Invoke(ctx, func(ctx context.Context, settings gax.CallSettings) error {")
	p(`    httpReq, err := http.NewRequest("%s", baseUrl.String(), %s)`, verb, maybeReqBytes)
	p("    if err != nil {")
	p(`      return err`)
	p("    }")
	// TODO: Should this http.Request use WithContext?
	p("    httpReq.Header = headers")
	p("")
	p("    httpRsp, err := c.httpClient.Do(httpReq)")
	p("    if err != nil{")
	p(`     return err`)
	p("    }")
	p("    defer httpRsp.Body.Close()")
	p("")
	p("    if err = googleapi.CheckResponse(httpRsp); err != nil {")
	p(`      return err`)
	p("    }")
	p("")
	p("    buf, err := ioutil.ReadAll(httpRsp.Body)")
	p("    if err != nil {")
	p(`      return err`)
	p("    }")
	p("")
	p("    if err := unm.Unmarshal(buf, resp); err != nil {")
	p("      return maybeUnknownEnum(err)")
	p("    }")
	p("")
	p("    return nil")
	p("  }, opts...)")
	p("  if e != nil {")
	p(`    return nil, "", e`)
	p("  }")
	p("  it.Response = resp")
	elems := g.maybeSortMapPage(elemField, pt)
	p("  return %s, resp.GetNextPageToken(), nil", elems)
	p("}")
	p("")
	g.makeFetchAndIterUpdate(pageSizeFieldName)
	p("}")

	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/api/iterator"}] = true
	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/protobuf/proto"}] = true
	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/protobuf/encoding/protojson"}] = true
	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/api/googleapi"}] = true
	g.imports[inSpec] = true
	g.imports[outSpec] = true

	return nil
}

func (g *generator) lroRESTCall(servName string, m *descriptor.MethodDescriptorProto) error {
	lowcaseServName := lowcaseRestClientName(servName)
	p := g.printf
	inType := g.descInfo.Type[m.GetInputType()].(*descriptor.DescriptorProto)
	// outType := g.descInfo.Type[m.GetOutputType()].(*descriptor.DescriptorProto)

	inSpec, err := g.descInfo.ImportSpec(inType)
	if err != nil {
		return err
	}

	// outSpec, err := g.descInfo.ImportSpec(outType)
	// if err != nil {
	// 	return err
	// }

	lroType := lroTypeName(m.GetName())
	p("func (c *%s) %s(ctx context.Context, req *%s.%s, opts ...gax.CallOption) (*%s, error) {",
		lowcaseServName, m.GetName(), inSpec.Name, inType.GetName(), lroType)
	p(`    return nil, fmt.Errorf("%s not yet supported for REST clients")`, m.GetName())
	p("}")
	p("")

	g.imports[pbinfo.ImportSpec{Path: "cloud.google.com/go/longrunning"}] = true

	return nil
}

func (g *generator) emptyUnaryRESTCall(servName string, m *descriptor.MethodDescriptorProto) error {
	info := getHTTPInfo(m)
	if info == nil {
		return errors.E(nil, "method has no http info: %s", m.GetName())
	}

	inType := g.descInfo.Type[m.GetInputType()]
	inSpec, err := g.descInfo.ImportSpec(inType)
	if err != nil {
		return err
	}

	p := g.printf
	lowcaseServName := lowcaseRestClientName(servName)
	p("func (c *%s) %s(ctx context.Context, req *%s.%s, opts ...gax.CallOption) error {",
		lowcaseServName, m.GetName(), inSpec.Name, inType.GetName())

	// TODO(dovs): handle cancellation, metadata, osv.
	// TODO(dovs): handle http headers
	// TODO(dovs): handle deadlines
	// TODO(dovs): handle call options

	body := "nil"
	verb := strings.ToUpper(info.verb)

	// Marshal body for HTTP methods that take a body.
	// TODO(dovs): add tests generating methods with(out) a request body.
	if info.body != "" {
		if verb == http.MethodGet || verb == http.MethodDelete {
			return fmt.Errorf("invalid use of body parameter for a get/delete method %q", m.GetName())
		}
		p("m := protojson.MarshalOptions{AllowPartial: true, UseProtoNames: false}")
		requestObject := "req"
		if info.body != "*" {
			requestObject = "body"
			p("body := req%s", fieldGetter(info.body))
		}
		p("jsonReq, err := m.Marshal(%s)", requestObject)
		p("if err != nil {")
		p("  return err")
		p("}")
		p("")
		body = "bytes.NewReader(jsonReq)"
		g.imports[pbinfo.ImportSpec{Path: "bytes"}] = true
		g.imports[pbinfo.ImportSpec{Path: "google.golang.org/protobuf/encoding/protojson"}] = true
	}

	g.generateURLString(m)
	g.generateQueryString(m)
	p("// Build HTTP headers from client and context metadata.")
	p(`headers := buildHeaders(ctx, c.xGoogMetadata, metadata.Pairs("Content-Type", "application/json"))`)
	p("return gax.Invoke(ctx, func(ctx context.Context, settings gax.CallSettings) error {")
	p(`  httpReq, err := http.NewRequest("%s", baseUrl.String(), %s)`, verb, body)
	p("  if err != nil {")
	p("      return err")
	p("  }")
	p("  httpReq = httpReq.WithContext(ctx)")
	p("  httpReq.Header = headers")
	p("")
	p("  httpRsp, err := c.httpClient.Do(httpReq)")
	p("  if err != nil{")
	p("   return err")
	p("  }")
	p("  defer httpRsp.Body.Close()")
	p("")
	p("  // Returns nil if there is no error, otherwise wraps")
	p("  // the response code and body into a non-nil error")
	p("  return googleapi.CheckResponse(httpRsp)")
	p("  }, opts...)")
	p("}")

	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/api/googleapi"}] = true
	g.imports[inSpec] = true
	return nil
}

func (g *generator) unaryRESTCall(servName string, m *descriptor.MethodDescriptorProto) error {
	info := getHTTPInfo(m)
	if info == nil {
		return errors.E(nil, "method has no http info: %s", m.GetName())
	}

	inType := g.descInfo.Type[m.GetInputType()]
	outType := g.descInfo.Type[m.GetOutputType()]

	inSpec, err := g.descInfo.ImportSpec(inType)
	if err != nil {
		return err
	}
	outSpec, err := g.descInfo.ImportSpec(outType)
	if err != nil {
		return err
	}
	outFqn := fmt.Sprintf("%s.%s", g.descInfo.ParentFile[outType].GetPackage(), outType.GetName())
	isHTTPBodyMessage := outFqn == "google.api.HttpBody"

	// Ignore error because the only possible error would be from looking up
	// the ImportSpec for the OutputType, which has already happened above.
	retTyp, _ := g.returnType(m)

	isCustomOp := g.isCustomOp(m, info)

	p := g.printf
	lowcaseServName := lowcaseRestClientName(servName)
	p("func (c *%s) %s(ctx context.Context, req *%s.%s, opts ...gax.CallOption) (%s, error) {",
		lowcaseServName, m.GetName(), inSpec.Name, inType.GetName(), retTyp)

	// TODO(dovs): handle cancellation, metadata, osv.
	// TODO(dovs): handle http headers
	// TODO(dovs): handle deadlines?
	// TODO(dovs): handle calloptions

	body := "nil"
	verb := strings.ToUpper(info.verb)

	// Marshal body for HTTP methods that take a body.
	// TODO(dovs): add tests generating methods with(out) a request body.
	if info.body != "" {
		if verb == http.MethodGet || verb == http.MethodDelete {
			return fmt.Errorf("invalid use of body parameter for a get/delete method %q", m.GetName())
		}
		p("m := protojson.MarshalOptions{AllowPartial: true}")
		requestObject := "req"
		if info.body != "*" {
			requestObject = "body"
			p("body := req%s", fieldGetter(info.body))
		}
		p("jsonReq, err := m.Marshal(%s)", requestObject)
		p("if err != nil {")
		p("  return nil, err")
		p("}")
		p("")

		body = "bytes.NewReader(jsonReq)"
		g.imports[pbinfo.ImportSpec{Path: "bytes"}] = true
	}

	// TOOD(dovs) reenable
	g.generateURLString(m)
	g.generateQueryString(m)
	p("// Build HTTP headers from client and context metadata.")
	p(`headers := buildHeaders(ctx, c.xGoogMetadata, metadata.Pairs("Content-Type", "application/json"))`)
	if !isHTTPBodyMessage {
		p("unm := protojson.UnmarshalOptions{AllowPartial: true, DiscardUnknown: true}")
	}
	p("resp := &%s.%s{}", outSpec.Name, outType.GetName())
	p("e := gax.Invoke(ctx, func(ctx context.Context, settings gax.CallSettings) error {")
	p(`  httpReq, err := http.NewRequest("%s", baseUrl.String(), %s)`, verb, body)
	p("  if err != nil {")
	p("      return err")
	p("  }")
	p("  httpReq = httpReq.WithContext(ctx)")
	p("  httpReq.Header = headers")
	p("")
	p("  httpRsp, err := c.httpClient.Do(httpReq)")
	p("  if err != nil{")
	p("   return err")
	p("  }")
	p("  defer httpRsp.Body.Close()")
	p("")
	p("  if err = googleapi.CheckResponse(httpRsp); err != nil {")
	p("    return err")
	p("  }")
	p("")
	p("  buf, err := ioutil.ReadAll(httpRsp.Body)")
	p("  if err != nil {")
	p("    return err")
	p("  }")
	p("")
	if isHTTPBodyMessage {
		p("resp.Data = buf")
		p(`if headers := httpRsp.Header; len(headers["Content-Type"]) > 0 {`)
		p(`  resp.ContentType = headers["Content-Type"][0]`)
		p("}")
	} else {
		p("if err := unm.Unmarshal(buf, resp); err != nil {")
		p("  return maybeUnknownEnum(err)")
		p("}")
		p("")
		p("return nil")
	}
	p("}, opts...)")
	p("if e != nil {")
	p("  return nil, e")
	p("}")
	ret := "return resp, nil"
	if isCustomOp {
		opVar := "op"
		g.customOpInit("resp", "req", opVar, inType.(*descriptor.DescriptorProto), g.customOpService(m))
		ret = fmt.Sprintf("return %s, nil", opVar)
	}
	p(ret)
	p("}")

	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/api/googleapi"}] = true
	g.imports[pbinfo.ImportSpec{Path: "google.golang.org/protobuf/encoding/protojson"}] = true
	g.imports[inSpec] = true
	g.imports[outSpec] = true
	return nil
}
