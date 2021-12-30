package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type gdClass struct {
	Name      string
	Parameter []string
}

type gdClient struct {
	Name    string
	Methods []Method
	Classes []gdClass
}

type Method struct {
	Name     string
	In       string
	Out      string
	Function string
}

type Params struct {
	raw      []string
	formated []string
}

func (p Params) String() string {
	return ""
}

func (p *Params) Parse(connStr string) (endpoint string, params []string, restMethod string) {
	connStr = strings.Split(connStr, " ")[0]
	connStrSplit := strings.Split(connStr, ":")
	log.Println("connStrSplit", connStrSplit)
	restMethod = connStrSplit[0]
	restPathTemplate := connStrSplit[1]
	log.Println("restPathTemplate", restPathTemplate)
	restPath := strings.Split(restPathTemplate, "{")

	endpoint = restPath[0]
	params = restPath[1:]

	if len(params) > 0 {
		p.raw = params
		p._clean()
		p._parse()
	}

	return endpoint, params, restMethod
}

func (p *Params) _clean() {
	p.formated = p.raw
	log.Println("before clean", p.raw)
	for i, path := range p.raw {
		p.formated[i] = strings.Trim(path, "}\"")
	}
	log.Println("after clean", p.formated)
}
func (p *Params) _parse() {
	for i, path := range p.raw {
		end := strings.Split(path, "}")[0]
		log.Println(path)
		fields := strings.Split(end, ".")
		for i, _ := range fields {
			fields[i] = strings.Title(fields[i])
		}
		p.formated[i] = "\"+str(In." + strings.Join(fields, ".") + ")"
	}
	log.Println("after", p.formated)
}

var extTypes *protoregistry.Types

func generateFile(p *protogen.Plugin, f *protogen.File) error {
	// Skip generating file if there is no message.
	if len(f.Messages) == 0 {
		return nil
	}

	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}

	// generate GDScript classes
	classes := []gdClass{}

	t, err := template.ParseFiles(filepath.Dir(ex) + "/templates/class.gd.template")
	if err != nil {
		log.Println("cannot read template:", err)
		os.Exit(1)
	}

	for _, msg := range f.Messages {
		filename := f.GeneratedFilenamePrefix + "_" + msg.GoIdent.GoName + ".gd"
		parameter := []string{}
		for _, field := range msg.Fields {
			parameter = append(parameter, field.GoName)
		}

		g := p.NewGeneratedFile(filename, f.GoImportPath)

		class := gdClass{Name: msg.GoIdent.GoName, Parameter: parameter}
		err = t.Execute(g, class)
		if err != nil {
			log.Println("cannot exec template:", err)
			os.Exit(1)
		}
		classes = append(classes, class)
	}

	// generate Loader
	t, err = template.ParseFiles(filepath.Dir(ex) + "/templates/loader.gd.template")
	if err != nil {
		log.Println("cannot read template:", err)
		os.Exit(1)
	}
	filename := f.GeneratedFilenamePrefix + "_Loader_" + *f.Proto.Package + ".gd"
	g := p.NewGeneratedFile(filename, f.GoImportPath)

	err = t.Execute(g, classes)
	if err != nil {
		log.Println("cannot exec template:", err)
		os.Exit(1)
	}

	// generate GDScript Clients
	t, err = template.ParseFiles(filepath.Dir(ex) + "/templates/client.gd.template")
	if err != nil {
		log.Println("cannot read template:", err)
		os.Exit(1)
	}

	// The type information for all extensions is in the source files,
	// so we need to extract them into a dynamically created protoregistry.Types.
	extTypes = new(protoregistry.Types)
	for _, file := range p.Files {
		if err := registerAllExtensions(extTypes, file.Desc); err != nil {
			panic(err)
		}
	}

	for _, svc := range f.Services {
		filename := f.GeneratedFilenamePrefix + "_Client_" + svc.GoName + ".gd"

		g := p.NewGeneratedFile(filename, f.GoImportPath)

		methods := []Method{}
		for _, method := range svc.Methods {
			newMethod := Method{
				Name: method.GoName,
				In:   method.Input.GoIdent.GoName,
				Out:  method.Output.GoIdent.GoName,
			}

			options := method.Desc.Options().(*descriptorpb.MethodOptions)
			connStr := extractOptions(options, "http")
			if connStr == "" {
				continue
			}
			log.Println("connStr", connStr)

			p := Params{}
			endpoint, params, restMethod := p.Parse(connStr)

			inProto := method.Input
			fields := "{"
			fds := inProto.Desc.Fields()

			//log.Print(fds)
			for i, field := range inProto.Fields {
				//log.Println(fds.Get(i).Kind())
				fields = fields + fmt.Sprintf("\"%s\": In.%s,", fds.Get(i).JSONName(), field.GoName)
			}
			fields = strings.Trim(fields, ",") + "}"

			newMethod.Function = fmt.Sprintf("\"%s\",%s,%s", strings.ToUpper(restMethod), endpoint+strings.Join(params, ""), fields)
			log.Print(newMethod)
			methods = append(methods, newMethod)
		}
		client := gdClient{Name: svc.GoName, Classes: classes, Methods: methods}
		err = t.Execute(g, client)
		if err != nil {
			log.Println("cannot exec template:", err)
			os.Exit(1)
		}
	}

	return nil

}

func extractOptions(options *descriptorpb.MethodOptions, fdName string) string {
	b, err := proto.Marshal(options)
	if err != nil {
		panic(err)
	}
	options.Reset()
	err = proto.UnmarshalOptions{Resolver: extTypes}.Unmarshal(b, options)
	if err != nil {
		panic(err)
	}

	connStr := ""
	connStrPtr := &connStr
	options.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if !fd.IsExtension() || string(fd.Name()) != fdName {
			return true
		}
		*connStrPtr = v.String()
		return true
	})
	if len(connStr) == 0 {
		return ""
	}
	return connStr
}

// Recursively register all extensions into the provided protoregistry.Types,
// starting with the protoreflect.FileDescriptor and recursing into its MessageDescriptors,
// their nested MessageDescriptors, and so on.
//
// This leverages the fact that both protoreflect.FileDescriptor and protoreflect.MessageDescriptor
// have identical Messages() and Extensions() functions in order to recurse through a single function
func registerAllExtensions(extTypes *protoregistry.Types, descs interface {
	Messages() protoreflect.MessageDescriptors
	Extensions() protoreflect.ExtensionDescriptors
}) error {
	mds := descs.Messages()
	for i := 0; i < mds.Len(); i++ {
		registerAllExtensions(extTypes, mds.Get(i))
	}
	xds := descs.Extensions()
	for i := 0; i < xds.Len(); i++ {
		if err := extTypes.RegisterExtension(dynamicpb.NewExtensionType(xds.Get(i))); err != nil {
			return err
		}
	}
	return nil
}
