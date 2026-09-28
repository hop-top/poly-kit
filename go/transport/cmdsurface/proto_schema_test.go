package cmdsurface_test

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

// TestProtoSchema_JSONKeysMatchGoTags pins the published schema to the
// Go types it mirrors: every JSON key a Go struct tag declares exists in
// the proto message under the same JSON name. The proto3 JSON mapping
// would otherwise emit lowerCamelCase (exitCode), and a JSON client
// written against the Go tags would read nothing.
func TestProtoSchema_JSONKeysMatchGoTags(t *testing.T) {
	cases := []struct {
		goType reflect.Type
		desc   protoreflect.MessageDescriptor
	}{
		{reflect.TypeFor[cmdsurface.Invocation](), (&cmdsurfacev1.Invocation{}).ProtoReflect().Descriptor()},
		{reflect.TypeFor[cmdsurface.Meta](), (&cmdsurfacev1.Meta{}).ProtoReflect().Descriptor()},
		{reflect.TypeFor[cmdsurface.Result](), (&cmdsurfacev1.Result{}).ProtoReflect().Descriptor()},
		{reflect.TypeFor[cmdsurface.Event](), (&cmdsurfacev1.Event{}).ProtoReflect().Descriptor()},
	}
	for _, tc := range cases {
		t.Run(tc.goType.Name(), func(t *testing.T) {
			if got, want := string(tc.desc.Name()), tc.goType.Name(); got != want {
				t.Errorf("proto message %q mirrors Go type %q", got, want)
			}
			byJSON := map[string]protoreflect.FieldDescriptor{}
			fields := tc.desc.Fields()
			for i := range fields.Len() {
				f := fields.Get(i)
				byJSON[f.JSONName()] = f
			}
			for i := range tc.goType.NumField() {
				sf := tc.goType.Field(i)
				key, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
				if key == "" || key == "-" {
					continue
				}
				f, ok := byJSON[key]
				if !ok {
					t.Errorf("Go field %s.%s (json %q) has no proto field with that JSON name",
						tc.goType.Name(), sf.Name, key)
					continue
				}
				if string(f.Name()) != key {
					t.Errorf("json %q maps to proto field %q; want the same snake_case name",
						key, f.Name())
				}
			}
		})
	}
}

// TestProtoSchema_ExitCodeHasPresence pins exit_code as a presence
// field: the proto3 JSON mapping omits a zero scalar, and a successful
// run must still report "exit_code": 0.
func TestProtoSchema_ExitCodeHasPresence(t *testing.T) {
	f := (&cmdsurfacev1.Result{}).ProtoReflect().Descriptor().Fields().ByName("exit_code")
	if f == nil {
		t.Fatal("Result has no exit_code field")
	}
	if !f.HasPresence() {
		t.Error("Result.exit_code has no presence; a zero exit code would vanish from JSON")
	}
}

// TestProtoSchema_ProcedurePaths pins the generated procedure paths to
// the ones the surface has always served.
func TestProtoSchema_ProcedurePaths(t *testing.T) {
	if got, want := cmdsurfacev1connect.CommandsInvokeProcedure, cmdsurface.RPCInvokeProcedure; got != want {
		t.Errorf("Invoke procedure %q, want %q", got, want)
	}
	if got, want := cmdsurfacev1connect.CommandsInvokeStreamProcedure, cmdsurface.RPCInvokeStreamProcedure; got != want {
		t.Errorf("InvokeStream procedure %q, want %q", got, want)
	}
	if got, want := "/"+cmdsurfacev1connect.CommandsName+"/", cmdsurface.RPCServicePath; got != want {
		t.Errorf("service path %q, want %q", got, want)
	}
}
