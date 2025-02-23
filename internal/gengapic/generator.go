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
	"os"
	"strings"
	"time"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/googleapis/gapic-generator-go/internal/errors"
	conf "github.com/googleapis/gapic-generator-go/internal/grpc_service_config"
	"github.com/googleapis/gapic-generator-go/internal/license"
	"github.com/googleapis/gapic-generator-go/internal/pbinfo"
	"github.com/googleapis/gapic-generator-go/internal/printer"
	"google.golang.org/genproto/googleapis/api/serviceconfig"
	metadatapb "google.golang.org/genproto/googleapis/gapic/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoiface"
	"gopkg.in/yaml.v2"
)

type generator struct {
	pt printer.P

	descInfo pbinfo.Info

	// Maps proto elements to their comments
	comments map[protoiface.MessageV1]string

	resp plugin.CodeGeneratorResponse

	imports map[pbinfo.ImportSpec]bool

	// Human-readable name of the API used in docs
	apiName string

	// Parsed service config from plugin option
	serviceConfig *serviceconfig.Service

	// gRPC ServiceConfig
	grpcConf conf.Config

	// Auxiliary types to be generated in the package
	aux *auxTypes

	// Options for the generator determining module names, transports,
	// config file paths, etc.
	opts *options

	// GapicMetadata for recording proto-to-code mappings in a
	// gapic_metadata.json file.
	metadata *metadatapb.GapicMetadata

	mixins mixins

	hasIAMPolicyOverrides bool

	// customOpServices is a map of service descriptors with methods that create custom operations
	// to the service descriptors of the custom operation services that manage those custom operation instances.
	customOpServices map[*descriptor.ServiceDescriptorProto]*descriptor.ServiceDescriptorProto
}

func (g *generator) init(req *plugin.CodeGeneratorRequest) error {
	g.metadata = &metadatapb.GapicMetadata{
		Schema:   "1.0",
		Language: "go",
		Comment:  "This file maps proto services/RPCs to the corresponding library clients/methods.",
		Services: make(map[string]*metadatapb.GapicMetadata_ServiceForTransport),
	}

	g.mixins = make(mixins)
	g.comments = map[protoiface.MessageV1]string{}
	g.imports = map[pbinfo.ImportSpec]bool{}
	g.customOpServices = map[*descriptor.ServiceDescriptorProto]*descriptor.ServiceDescriptorProto{}
	g.aux = &auxTypes{
		iters: map[string]*iterType{},
		lros:  map[*descriptor.MethodDescriptorProto]bool{},
	}

	opts, err := parseOptions(req.Parameter)
	if err != nil {
		return err
	}
	files := req.GetProtoFile()

	if opts.serviceConfigPath != "" {
		f, err := os.Open(opts.serviceConfigPath)
		if err != nil {
			return errors.E(nil, "error opening service config: %v", err)
		}
		defer f.Close()

		g.serviceConfig = &serviceconfig.Service{}
		err = yaml.NewDecoder(f).Decode(g.serviceConfig)
		if err != nil {
			return errors.E(nil, "error decoding service config: %v", err)
		}

		// An API Service Config will always have a `name` so if it is not populated,
		// it's an invalid config.
		if g.serviceConfig.GetName() == "" {
			return errors.E(nil, "invalid API service config file %q", opts.serviceConfigPath)
		}

		g.collectMixins()
		files = append(files, g.getMixinFiles()...)
	}
	if opts.grpcConfPath != "" {
		f, err := os.Open(opts.grpcConfPath)
		if err != nil {
			return errors.E(nil, "error opening gRPC service config: %v", err)
		}
		defer f.Close()

		g.grpcConf, err = conf.New(f)
		if err != nil {
			return errors.E(nil, "error parsing gPRC service config: %v", err)
		}
	}
	g.opts = opts

	g.descInfo = pbinfo.Of(files)

	for _, f := range files {
		for _, loc := range f.GetSourceCodeInfo().GetLocation() {
			if loc.LeadingComments == nil {
				continue
			}

			// p is an array with format [f1, i1, f2, i2, ...]
			// - f1 refers to the protobuf field tag
			// - if field refer to by f1 is a slice, i1 refers to an element in that slice
			// - f2 and i2 works recursively.
			// So, [6, x] refers to the xth service defined in the file,
			// since the field tag of Service is 6.
			// [6, x, 2, y] refers to the yth method in that service,
			// since the field tag of Method is 2.
			p := loc.Path
			switch {
			case len(p) == 2 && p[0] == 6:
				g.comments[f.Service[p[1]]] = *loc.LeadingComments
			case len(p) == 4 && p[0] == 6 && p[2] == 2:
				g.comments[f.Service[p[1]].Method[p[3]]] = *loc.LeadingComments
			}
		}
	}

	return nil
}

// printf formatted-prints to sb, using the print syntax from fmt package.
//
// It automatically keeps track of indentation caused by curly-braces.
// To make nested blocks easier to write elsewhere in the code,
// leading and trailing whitespaces in s are ignored.
// These spaces are for humans reading the code, not machines.
//
// Currently it's not terribly difficult to confuse the auto-indenter.
// To fix-up, manipulate g.in or write to g.sb directly.
func (g *generator) printf(s string, a ...interface{}) {
	g.pt.Printf(s, a...)
}

func (g *generator) commit(fileName, pkgName string) {
	var header strings.Builder
	fmt.Fprintf(&header, license.Apache, time.Now().Year())
	fmt.Fprintf(&header, "package %s\n\n", pkgName)

	var imps []pbinfo.ImportSpec
	for imp := range g.imports {
		imps = append(imps, imp)
	}
	impDiv := sortImports(imps)

	writeImp := func(is pbinfo.ImportSpec) {
		s := "\t%[2]q\n"
		if is.Name != "" {
			s = "\t%s %q\n"
		}
		fmt.Fprintf(&header, s, is.Name, is.Path)
	}

	header.WriteString("import (\n")
	for _, imp := range imps[:impDiv] {
		writeImp(imp)
	}
	if impDiv != 0 && impDiv != len(imps) {
		header.WriteByte('\n')
	}
	for _, imp := range imps[impDiv:] {
		writeImp(imp)
	}
	header.WriteString(")\n\n")

	g.resp.File = append(g.resp.File, &plugin.CodeGeneratorResponse_File{
		Name:    &fileName,
		Content: proto.String(header.String()),
	})

	// Trim trailing newlines so we have only one.
	// NOTE(pongad): This might be an overkill since we have gofmt,
	// but the rest of the file already conforms to gofmt, so we might as well?
	body := g.pt.String()
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] != '\n' {
			body = body[:i+2]
			break
		}
	}

	g.resp.File = append(g.resp.File, &plugin.CodeGeneratorResponse_File{
		Content: proto.String(body),
	})
}

func (g *generator) reset() {
	g.pt.Reset()
	for k := range g.imports {
		delete(g.imports, k)
	}
}

// fqn recursively builds the fully qualified proto element name,
// but omits the leading ".". For example, google.foo.v1.FooMessage.
func (g *generator) fqn(p pbinfo.ProtoType) string {
	// Base case. Use proto package instead of relative file name.
	if f, isFile := p.(*descriptor.FileDescriptorProto); isFile {
		return f.GetPackage()
	}

	parent := g.descInfo.ParentElement[p]
	if parent == nil {
		parent = g.descInfo.ParentFile[p]
	}
	return fmt.Sprintf("%s.%s", g.fqn(parent), p.GetName())
}

func (g *generator) nestedName(nested pbinfo.ProtoType) string {
	name := nested.GetName()

	parent, hasParent := g.descInfo.ParentElement[nested]
	for hasParent {
		name = fmt.Sprintf("%s_%s", parent.GetName(), name)
		parent, hasParent = g.descInfo.ParentElement[parent]
	}

	return name
}
