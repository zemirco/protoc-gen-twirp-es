package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
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
export const {{.Name}} = async (input: {{.InputType}}): Promise<{{.OutputType}}> => {
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
	const data = await res.json()
	return new {{.OutputType}}(data)
}
{{end}}
`

var classes = []string{
	"export class Empty extends Object{}\n",
	"\n",
	"export class StringValue extends String{}\n",
	"\n",
	"export class UInt64Value extends Number{}\n",
}

const class = `
export class {{.Message.GetName}} {
{{- $Message := .Message -}}
{{- range $i, $field := .Message.GetField}}
  {{.Name}}: {{getTypeScriptType $Message .}}
{{- end}}
  constructor(o) {
    {{- range $i, $field := .Message.GetField}}
    {{initiate $Message .}}
    {{- end}}
  }
}
`

// Method comment
type Method struct {
	Service    string
	Name       string
	OutputType string
	InputType  string
}

// Methods comment
var Methods []Method

func isBuiltIn(name string) bool {
	switch name {
	case
		"Empty",
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
	"initiate":          initiate,
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

			if !isBuiltIn(message.GetName()) {
				parsed := template.Must(template.New("").Funcs(funcMap).Parse(class))
				data := struct {
					Message *descriptor.DescriptorProto
				}{
					Message: message,
				}
				var tmp bytes.Buffer
				if err := parsed.Execute(&tmp, data); err != nil {
					panic(err)
				}
				classes = append(classes, tmp.String())
			}

			// generate key, e.g. ".trpc.MatchesPoints"
			key := "." + f.GetPackage() + "." + message.GetName()
			messages[key] = message

			// get nested types for maps, i.e. <string, Something>
			for _, t := range message.GetNestedType() {
				subkey := key + "." + t.GetName()
				messages[subkey] = t
			}
		}

		// services
		for _, service := range f.Service {

			// methods
			for _, method := range service.Method {

				outputType := messages[method.GetOutputType()]
				inputType := messages[method.GetInputType()]

				m := Method{
					Service:    service.GetName(),
					Name:       method.GetName(),
					OutputType: outputType.GetName(),
					InputType:  inputType.GetName(),
				}

				Methods = append(Methods, m)
			}

		}
	}

	parsed := template.Must(template.New("").Parse(blueprint))
	data := struct {
		Methods []Method
	}{
		Methods: Methods,
	}
	var tmp bytes.Buffer
	if err := parsed.Execute(&tmp, data); err != nil {
		panic(err)
	}

	// generate file with functions
	name := strings.Replace(req.FileToGenerate[0], ".proto", ".ts", -1)
	content := strings.Join(classes, "") + tmp.String()
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

func initiate(message *descriptor.DescriptorProto, field *descriptor.FieldDescriptorProto) string {
	// object string: custom Type, e.g. stats: { [name: string]: Stats }
	if isMap(field.GetTypeName()) {
		msg := message.GetNestedType()[0]
		fields := msg.GetField()
		value := fields[1]
		return fmt.Sprintf("this.%s = Object.entries(o.%s).reduce((a, [k, v]) => {a[k] = new %s(v || {}); return a}, {})", field.GetName(), field.GetName(), getTypeScriptType(msg, value))
	}
	// array of primitive values or custom types, e.g. number[] or Follower[]
	if isRepeated(field.GetLabel()) {
		return fmt.Sprintf("this.%s = o.%s || []", field.GetName(), field.GetName())
	}
	// timestamp
	if isTimestamp(field.GetTypeName()) {
		return fmt.Sprintf("this.%s = o.%s || \"\"", field.GetName(), field.GetName())
	}
	// custom type, e.g. Match
	if isMessage(field.GetType()) {
		parts := strings.Split(field.GetTypeName(), ".")
		return fmt.Sprintf("this.%s = new %s(o.%s || {})", field.GetName(), parts[len(parts)-1], field.GetName())
	}
	// primitive value
	return fmt.Sprintf("this.%s = o.%s || %s", field.GetName(), field.GetName(), zv(field.GetType()))
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

// handle well known type .google.protobuf.Timestamp
// has fields "seconds" and "nanos" but is single string in JSON
func isTimestamp(typeName string) bool {
	return typeName == ".google.protobuf.Timestamp"
}
