package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"text/template"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
)

var messages = make(map[string]*descriptor.DescriptorProto)

const blueprint = `
{{- range $i, $method := .Methods}}
const {{.Name}} = async (input: {{.InputType}}): Promise<{{.OutputType}}> => {
	const meta = document.querySelector('meta[name="csrf-token"]') as HTMLMetaElement
	const token = meta.content
	const res = await fetch('/twirp/trpc.{{.Service}}/{{.Name}}', {
		headers: {
			'X-CSRF-Token': token,
			'Content-Type': 'application/json'
		},
		credentials: 'same-origin',
		method: 'POST',
		body: JSON.stringify(input)
	})
	if (res.status !== 200) {
		throw new Error(res.statusText)
	}
	const data = await res.json() as {{.OutputType}}

	{{.Field}}
	return {{.OutputName}}
}
{{end}}
{{.Exports}}
`

const stuff = `
interface {{.Message.GetName}} {
	{{ $Message := .Message }}
	{{- range $i, $field := .Message.GetField}}
		{{.Name}}: {{getTypeScriptType $Message .}}
	{{- end}}
}
`

// Method comment
type Method struct {
	Service    string
	Name       string
	OutputName string
	Field      string
	OutputType string
	InputType  string
}

// Methods comment
var Methods []Method

func isBuiltIn(name string) bool {
	switch name {
	case
		"Timestamp",
		"DoubleValue",
		"FloatValue",
		"Int64Value",
		"UInt64Value",
		"Int32Value",
		"Fixed64Value",
		"Fixed32Value",
		"BoolValue",
		"StringValue",
		"GroupValue",
		"MessageValue",
		"BytesValue",
		"UInt32Value",
		"EnumValue",
		"Sfixed32Value",
		"Sfixed64Value",
		"Sint32Value",
		"Sint64Value":
		return true
	default:
		return false
	}
}

var funcMap = template.FuncMap{
	"getTypeScriptType": getTypeScriptType,
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

			if isBuiltIn(message.GetName()) {
				continue
			}

			parsed := template.Must(template.New("").Funcs(funcMap).Parse(stuff))
			data := struct {
				Message *descriptor.DescriptorProto
			}{
				Message: message,
			}
			var tmp bytes.Buffer
			if err := parsed.Execute(&tmp, data); err != nil {
				panic(err)
			}
			log.Println(tmp.String())
			log.Println("---")

			continue

			// // generate key, e.g. ".trpc.MatchesPoints"
			// key := "." + f.GetPackage() + "." + message.GetName()
			// messages[key] = message

			// // get nested types for maps, i.e. <string, Something>
			// for _, t := range message.GetNestedType() {
			// 	subkey := key + "." + t.GetName()
			// 	messages[subkey] = t
			// }
		}

		// services
		for _, service := range f.Service {

			// methods
			for _, method := range service.Method {

				outputType := messages[method.GetOutputType()]
				inputType := messages[method.GetInputType()]
				var s bytes.Buffer

				var m Method

				if isPrimitive(outputType.GetName()) {
					// return result directly
					// e.g.
					// 	const data = await res.json()
					// 	return data
					m = Method{
						Service:    service.GetName(),
						Name:       method.GetName(),
						OutputName: "data",
						OutputType: outputType.GetName(),
						InputType:  inputType.GetName(),
					}
				} else {
					// open type json
					s.WriteString(fmt.Sprintf("const %s = {\n", outputType.GetName()))
					// generate fields
					// "data" comes from template which holds json from fetch call
					s.WriteString(genField(outputType.Field, "data"))
					// close type json
					s.WriteString("}\n")
					m = Method{
						Service:    service.GetName(),
						Name:       method.GetName(),
						OutputName: outputType.GetName(),
						Field:      s.String(),
						OutputType: outputType.GetName(),
						InputType:  inputType.GetName(),
					}
				}

				Methods = append(Methods, m)
			}

		}
	}

	parsed := template.Must(template.New("").Parse(blueprint))
	data := struct {
		Methods []Method
		// ServiceName string
		Exports string
	}{
		Methods: Methods,
		// ServiceName: "Haberdasher",
		Exports: export(),
	}
	var tmp bytes.Buffer
	if err := parsed.Execute(&tmp, data); err != nil {
		panic(err)
	}

	name := strings.Replace(req.FileToGenerate[0], ".proto", ".ts", -1)
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

		if isTimestamp(f.GetTypeName()) {
			ids := append(id, f.GetName())
			s := strings.Join(ids, ".")
			b.WriteString(fmt.Sprintf("%s: %s || \"\"%s\n", f.GetName(), s, colon))
		} else if isMap(f.GetTypeName()) {
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
			b.WriteString(fmt.Sprintf("%s: %s ? %s.map(v => {\n", f.GetName(), joined, joined))
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
			b.WriteString(fmt.Sprintf("}) : []%s\n", colon))
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

func getTypeScriptType(message *descriptor.DescriptorProto, field *descriptor.FieldDescriptorProto) string {
	var result string
	switch field.GetType() {
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
		result = "number"
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		result = "boolean"
	case descriptor.FieldDescriptorProto_TYPE_STRING:
		result = "string"
	default:
		if isTimestamp(field.GetTypeName()) {
			result = "string"
		} else if isMap(field.GetTypeName()) {
			msg := message.GetNestedType()[0]
			fields := msg.GetField()
			key := fields[0]
			value := fields[1]
			result = fmt.Sprintf("{ [name: %s]: %s }", getTypeScriptType(msg, key), getTypeScriptType(msg, value))
		} else {
			parts := strings.Split(field.GetTypeName(), ".")
			result = parts[len(parts)-1]
		}
	}
	if isRepeated(field.GetLabel()) && !isMap(field.GetTypeName()) {
		result += "[]"
	}
	return result
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

func isPrimitive(name string) bool {
	return name == "StringValue"
}

// handle well known type .google.protobuf.Timestamp
// has fields "seconds" and "nanos" but is single string in JSON
func isTimestamp(typeName string) bool {
	return typeName == ".google.protobuf.Timestamp"
}
