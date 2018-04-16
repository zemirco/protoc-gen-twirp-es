package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"text/template"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
)

var messages = make(map[string]*descriptor.DescriptorProto)

const blueprint = `
{{$ServiceName := .ServiceName}}
{{- range $i, $method := .Methods}}
const {{.Name}} = async (input) => {
	const token = document.querySelector('meta[name="csrf-token"]').content
	const res = await fetch('/twirp/trpc.{{$ServiceName}}/{{.Name}}', {
		headers: {
			'X-CSRF-Token': token,
			'Content-Type': 'application/json'
		},
		credentials: 'same-origin',
		method: 'POST',
		body: JSON.stringify(input)
	})
	const data = await res.json()
	{{.Field}}
	return {{.OutputName}}
}
{{end}}
{{.Exports}}
`

// Method comment
type Method struct {
	Name       string
	OutputName string
	Field      string
}

// Methods comment
var Methods []Method

func getTypeName(s string) string {
	parts := strings.Split(s, ".")
	return parts[len(parts)-1]
}

func main() {
	in, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	req := &plugin.CodeGeneratorRequest{}
	if err := proto.Unmarshal(in, req); err != nil {
		panic(err)
	}

	for _, f := range req.ProtoFile {
		// messages
		for _, message := range f.MessageType {
			var s bytes.Buffer

			// log.Println(message)
			s.WriteString(fmt.Sprintf("class %s {\n", message.GetName()))

			// log.Println(message.GetField())
			// generate constructor
			s.WriteString("  constructor (o) {\n")
			for _, field := range message.GetField() {
				log.Println(field)
				if isMessage(field.GetType()) {
					s.WriteString(fmt.Sprintf("    this.%s = new %s(o.%s)\n", field.GetName(), getTypeName(field.GetTypeName()), field.GetName()))
				} else {
					s.WriteString(fmt.Sprintf("    this.%s = o.%s || %s\n", field.GetName(), field.GetName(), zv(field.GetType())))
				}
			}
			s.WriteString("  }\n")

			// generate setters
			// for _, field := range message.GetField() {
			// 	// log.Println("field: ", field)
			// 	s.WriteString(fmt.Sprintf("  get %s () {return this.%s || %s}\n", field.GetName(), field.GetName(), zv(field.GetType())))
			// }

			s.WriteString(fmt.Sprintf("}\n"))

			log.Println(s.String())
			// generate key, e.g. ".trpc.MatchesPoints"
			key := "." + f.GetPackage() + "." + message.GetName()
			messages[key] = message

			// get nested types for maps, i.e. <string, Something>
			for _, t := range message.GetNestedType() {
				subkey := key + "." + t.GetName()
				messages[subkey] = t
				// log.Println(t)
			}
		}

		// services
		for _, service := range f.Service {

			// methods
			for _, method := range service.Method {

				outputType := messages[method.GetOutputType()]
				var s bytes.Buffer

				// open type json
				s.WriteString(fmt.Sprintf("const %s = {\n", outputType.GetName()))

				// generate fields
				// "data" comes from template which holds json from fetch call
				s.WriteString(genField(outputType.Field, "data"))

				// close type json
				s.WriteString("}\n")

				m := Method{
					Name:       method.GetName(),
					OutputName: outputType.GetName(),
					Field:      s.String(),
				}
				Methods = append(Methods, m)
			}

		}
	}

	parsed := template.Must(template.New("").Parse(blueprint))
	data := struct {
		Methods     []Method
		ServiceName string
		Exports     string
	}{
		Methods:     Methods,
		ServiceName: "Haberdasher",
		Exports:     export(),
	}
	var tmp bytes.Buffer
	if err := parsed.Execute(&tmp, data); err != nil {
		panic(err)
	}

	name := strings.Replace(req.FileToGenerate[0], ".proto", ".js", -1)
	content := tmp.String()
	res := &plugin.CodeGeneratorResponse{}
	res.File = append(res.File, &plugin.CodeGeneratorResponse_File{
		Name:    &name,
		Content: &content,
	})
	out, err := proto.Marshal(res)
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		panic(err)
	}
}

func genField(ff []*descriptor.FieldDescriptorProto, id ...string) string {
	var b bytes.Buffer
	for i, f := range ff {

		// apped colon when not the last field
		colon := ","
		if len(ff)-1 == i {
			colon = ""
		}

		// get field type from type map
		m := messages[f.GetTypeName()]
		if isMap(f.GetTypeName()) {
			nested := messages[f.GetTypeName()]
			nestedValue := nested.GetField()[1]
			nestedValueType := messages[nestedValue.GetTypeName()]

			ids := append(id, f.GetName())
			j := strings.Join(ids, ".")

			// start loop
			b.WriteString(fmt.Sprintf("%s: Object.entries(%s).reduce((a, [k, v]) => {\n", f.GetName(), j))
			b.WriteString(fmt.Sprintf("a[k] = {\n"))

			// generate fields
			b.WriteString(genField(nestedValueType.GetField(), []string{"v"}...))

			// end loop
			b.WriteString("}\n")
			b.WriteString("return a\n")
			b.WriteString(fmt.Sprintf("}, {})%s\n", colon))
		} else if isRepeated(f.GetLabel()) {
			ids := append(id, f.GetName())
			joined := strings.Join(ids, ".")

			// start javascript map function
			b.WriteString(fmt.Sprintf("%s: %s.map(v => {\n", f.GetName(), joined))
			fields := m.GetField()
			if len(fields) == 0 {
				// array of primitive values
				b.WriteString(fmt.Sprintf("return v || %s\n", zv(f.GetType())))
			} else {
				// array of complex values, i.e. objects

				// open javascript object
				b.WriteString("return {\n")

				// generate fields
				ids := []string{"v"}
				b.WriteString(genField(fields, ids...))

				// close javascript object
				b.WriteString("}\n")
			}

			// close javascript map function
			b.WriteString(fmt.Sprintf("})%s\n", colon))
		} else if isMessage(f.GetType()) {
			ids := append(id, f.GetName())

			// open javascript object for nested fields
			b.WriteString(fmt.Sprintf("%s: {\n", f.GetName()))

			// generate content of nested fields by calling genField recursively
			b.WriteString(genField(m.GetField(), ids...))

			// close javascript object for nested fields
			b.WriteString(fmt.Sprintf("}%s\n", colon))
		} else {

			// write simple json line
			// e.g. "key: value || 0"
			ids := append(id, f.GetName())
			s := strings.Join(ids, ".")
			b.WriteString(fmt.Sprintf("%s: %s || %s%s\n", f.GetName(), s, zv(f.GetType()), colon))
		}
	}
	return b.String()
}

// return zero value for primitive type
func zv(t descriptor.FieldDescriptorProto_Type) string {
	switch t {
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE,
		descriptor.FieldDescriptorProto_TYPE_FLOAT,
		descriptor.FieldDescriptorProto_TYPE_INT64,
		descriptor.FieldDescriptorProto_TYPE_UINT64,
		descriptor.FieldDescriptorProto_TYPE_INT32,
		descriptor.FieldDescriptorProto_TYPE_FIXED64,
		descriptor.FieldDescriptorProto_TYPE_FIXED32,
		descriptor.FieldDescriptorProto_TYPE_UINT32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED64,
		descriptor.FieldDescriptorProto_TYPE_SINT32,
		descriptor.FieldDescriptorProto_TYPE_SINT64:
		return "0"
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		return "false"
	case descriptor.FieldDescriptorProto_TYPE_STRING:
		return "\"\""
	default:
		return "{}"
	}
}

// generate last export line for javascript file
// e.g. "export {MyMethod, MyOtherMethod}"
func export() string {
	var b bytes.Buffer
	b.WriteString("export {")
	names := []string{}
	for _, method := range Methods {
		names = append(names, method.Name)
	}
	b.WriteString(strings.Join(names, ", "))
	b.WriteString("}")
	return b.String()
}

func isRepeated(label descriptor.FieldDescriptorProto_Label) bool {
	return label == descriptor.FieldDescriptorProto_LABEL_REPEATED
}

func isMessage(t descriptor.FieldDescriptorProto_Type) bool {
	return t == descriptor.FieldDescriptorProto_TYPE_MESSAGE
}

func isMap(typeName string) bool {
	return strings.HasSuffix(typeName, "Entry")
}
