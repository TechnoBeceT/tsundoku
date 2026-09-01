package api

import (
	"reflect"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"
)

const mutationCatalogUnavailableDescription = "Source catalog unavailable. If live source validation fails, no mutation is persisted. If post-commit effective-configuration composition fails, the mutation may already be persisted."

func TestSourceConfigurationContract(t *testing.T) {
	doc := decodeOpenAPI(t)
	schemas := objectAt(t, objectAt(t, doc, "components"), "schemas")

	t.Run("schemas", func(t *testing.T) {
		assertObjectSchema(t, schemas, "SourceIdentity", []string{"sourceId", "name", "language"}, []string{"sourceId", "name", "language"})
		assertType(t, propertyAt(t, schemas, "SourceIdentity", "sourceId"), "string")
		assertType(t, propertyAt(t, schemas, "SourceIdentity", "name"), "string")
		assertType(t, propertyAt(t, schemas, "SourceIdentity", "language"), "string")

		assertPolicyValue(t, schemas, "IntegerPolicyValue", []any{"integer", "null"}, "integer")
		assertPolicyValue(t, schemas, "DurationPolicyValue", []any{"string", "null"}, "string")
		assertPolicyValue(t, schemas, "BooleanPolicyValue", []any{"boolean", "null"}, "boolean")

		assertObjectSchema(t, schemas, "ImageConnectionPolicyValue", []string{"override", "global", "effective", "inherited"}, []string{"override", "global", "effective", "inherited"})
		assertTypes(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "override"), []any{"string", "null"})
		assertEnum(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "override"), []any{"fresh", "reuse", nil})
		assertType(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "global"), "string")
		assertEnum(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "global"), []any{"fresh", "reuse"})
		assertType(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "effective"), "string")
		assertEnum(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "effective"), []any{"fresh", "reuse"})
		assertType(t, propertyAt(t, schemas, "ImageConnectionPolicyValue", "inherited"), "boolean")
		assertObjectSchema(t, schemas, "KCEFPolicyValue", []string{"override", "global", "effective", "inherited", "enabled"}, []string{"override", "global", "effective", "inherited", "enabled"})
		assertTypes(t, propertyAt(t, schemas, "KCEFPolicyValue", "override"), []any{"string", "null"})
		assertEnum(t, propertyAt(t, schemas, "KCEFPolicyValue", "override"), []any{"auto", "required", "disabled", nil})
		assertEnum(t, propertyAt(t, schemas, "KCEFPolicyValue", "global"), []any{"auto"})
		assertEnum(t, propertyAt(t, schemas, "KCEFPolicyValue", "effective"), []any{"auto", "required", "disabled"})
		assertType(t, propertyAt(t, schemas, "KCEFPolicyValue", "inherited"), "boolean")
		assertType(t, propertyAt(t, schemas, "KCEFPolicyValue", "enabled"), "boolean")

		assertObjectSchema(t, schemas, "BypassSessionPolicyValue", []string{"override", "global", "effective", "inherited", "mode"}, []string{"override", "global", "effective", "inherited", "mode"})
		assertTypes(t, propertyAt(t, schemas, "BypassSessionPolicyValue", "override"), []any{"boolean", "null"})
		assertType(t, propertyAt(t, schemas, "BypassSessionPolicyValue", "global"), "boolean")
		assertType(t, propertyAt(t, schemas, "BypassSessionPolicyValue", "effective"), "boolean")
		assertType(t, propertyAt(t, schemas, "BypassSessionPolicyValue", "inherited"), "boolean")
		assertEnum(t, propertyAt(t, schemas, "BypassSessionPolicyValue", "mode"), []any{"disabled", "disposable", "reusable"})

		protectionFields := []string{"warmupInterval", "warmupSlowThresholdMs", "failureThreshold", "sourceCooldown", "politenessDelay"}
		assertObjectSchema(t, schemas, "SourceProtectionConfiguration", protectionFields, protectionFields)
		assertType(t, propertyAt(t, schemas, "SourceProtectionConfiguration", "warmupInterval"), "string")
		assertType(t, propertyAt(t, schemas, "SourceProtectionConfiguration", "warmupSlowThresholdMs"), "integer")
		assertType(t, propertyAt(t, schemas, "SourceProtectionConfiguration", "failureThreshold"), "integer")
		assertType(t, propertyAt(t, schemas, "SourceProtectionConfiguration", "sourceCooldown"), "string")
		assertType(t, propertyAt(t, schemas, "SourceProtectionConfiguration", "politenessDelay"), "string")

		proxyFields := []string{"optedIn", "gatewayEnabled", "gatewayConfigured", "effectiveAvailable"}
		assertObjectSchema(t, schemas, "SourceImageProxyState", proxyFields, proxyFields)
		for _, field := range proxyFields {
			assertType(t, propertyAt(t, schemas, "SourceImageProxyState", field), "boolean")
		}

		assertObjectSchema(t, schemas, "ResolvedEndpoint", []string{"endpointId", "name"}, []string{"endpointId", "name"})
		assertTypes(t, propertyAt(t, schemas, "ResolvedEndpoint", "endpointId"), []any{"string", "null"})
		assertTypes(t, propertyAt(t, schemas, "ResolvedEndpoint", "name"), []any{"string", "null"})

		storedRoutingFields := []string{"configured", "socksMode", "socks", "bypassMode", "bypass"}
		assertObjectSchema(t, schemas, "SourceStoredRoutingConfiguration", storedRoutingFields, storedRoutingFields)
		assertType(t, propertyAt(t, schemas, "SourceStoredRoutingConfiguration", "configured"), "boolean")
		assertEnum(t, propertyAt(t, schemas, "SourceStoredRoutingConfiguration", "socksMode"), []any{"global", "endpoint"})
		assertRef(t, propertyAt(t, schemas, "SourceStoredRoutingConfiguration", "socks"), "#/components/schemas/ResolvedEndpoint")
		assertEnum(t, propertyAt(t, schemas, "SourceStoredRoutingConfiguration", "bypassMode"), []any{"none", "global", "endpoint"})
		assertRef(t, propertyAt(t, schemas, "SourceStoredRoutingConfiguration", "bypass"), "#/components/schemas/ResolvedEndpoint")

		assertObjectSchema(t, schemas, "SourceRoutingConfiguration", []string{"stored", "socksMode", "socks", "bypassMode", "bypass"}, []string{"stored", "socksMode", "socks", "bypassMode", "bypass"})
		assertRef(t, propertyAt(t, schemas, "SourceRoutingConfiguration", "stored"), "#/components/schemas/SourceStoredRoutingConfiguration")
		assertEnum(t, propertyAt(t, schemas, "SourceRoutingConfiguration", "socksMode"), []any{"global", "endpoint"})
		assertRef(t, propertyAt(t, schemas, "SourceRoutingConfiguration", "socks"), "#/components/schemas/ResolvedEndpoint")
		assertEnum(t, propertyAt(t, schemas, "SourceRoutingConfiguration", "bypassMode"), []any{"none", "global", "endpoint"})
		assertRef(t, propertyAt(t, schemas, "SourceRoutingConfiguration", "bypass"), "#/components/schemas/ResolvedEndpoint")

		runtimeFields := []string{"status", "desiredRevision", "appliedRevision", "lastApplyAttempt", "lastApplyError"}
		assertObjectSchema(t, schemas, "SourceRuntimeStatus", runtimeFields, runtimeFields)
		assertEnum(t, propertyAt(t, schemas, "SourceRuntimeStatus", "status"), []any{"applied", "pending"})
		assertTypeAndFormat(t, propertyAt(t, schemas, "SourceRuntimeStatus", "desiredRevision"), "integer", "int64")
		assertTypeAndFormat(t, propertyAt(t, schemas, "SourceRuntimeStatus", "appliedRevision"), "integer", "int64")
		assertTypes(t, propertyAt(t, schemas, "SourceRuntimeStatus", "lastApplyAttempt"), []any{"string", "null"})
		assertValue(t, propertyAt(t, schemas, "SourceRuntimeStatus", "lastApplyAttempt"), "format", "date-time")
		assertType(t, propertyAt(t, schemas, "SourceRuntimeStatus", "lastApplyError"), "string")

		assertObjectSchema(t, schemas, "SourceExceptionSummary", []string{"source", "exceptionCount", "runtime"}, []string{"source", "exceptionCount", "runtime"})
		assertRef(t, propertyAt(t, schemas, "SourceExceptionSummary", "source"), "#/components/schemas/SourceIdentity")
		assertType(t, propertyAt(t, schemas, "SourceExceptionSummary", "exceptionCount"), "integer")
		assertValue(t, propertyAt(t, schemas, "SourceExceptionSummary", "exceptionCount"), "minimum", 0)
		assertRef(t, propertyAt(t, schemas, "SourceExceptionSummary", "runtime"), "#/components/schemas/SourceRuntimeStatus")

		configurationFields := []string{"source", "downloadConcurrency", "imageRequestDelay", "protection", "bypassEnabled", "reuseBypassSession", "imageConnectionMode", "kcef", "imageProxy", "routing", "profileKey", "runtime"}
		assertObjectSchema(t, schemas, "SourceEffectiveConfiguration", configurationFields, configurationFields)
		configurationRefs := map[string]string{
			"source":              "SourceIdentity",
			"downloadConcurrency": "IntegerPolicyValue",
			"imageRequestDelay":   "DurationPolicyValue",
			"protection":          "SourceProtectionConfiguration",
			"reuseBypassSession":  "BypassSessionPolicyValue",
			"imageConnectionMode": "ImageConnectionPolicyValue",
			"kcef":                "KCEFPolicyValue",
			"imageProxy":          "SourceImageProxyState",
			"routing":             "SourceRoutingConfiguration",
			"runtime":             "SourceRuntimeStatus",
		}
		for field, schema := range configurationRefs {
			assertRef(t, propertyAt(t, schemas, "SourceEffectiveConfiguration", field), "#/components/schemas/"+schema)
		}
		assertType(t, propertyAt(t, schemas, "SourceEffectiveConfiguration", "bypassEnabled"), "boolean")
		assertType(t, propertyAt(t, schemas, "SourceEffectiveConfiguration", "profileKey"), "string")

		assertEnumSchema(t, schemas, "PolicyPatchMode", []any{"inherit", "override"})
		assertObjectSchema(t, schemas, "BooleanPolicyPatch", []string{"mode", "value"}, []string{"mode"})
		assertValue(t, objectAt(t, schemas, "BooleanPolicyPatch"), "additionalProperties", false)
		assertRef(t, propertyAt(t, schemas, "BooleanPolicyPatch", "mode"), "#/components/schemas/PolicyPatchMode")
		assertType(t, propertyAt(t, schemas, "BooleanPolicyPatch", "value"), "boolean")
		assertObjectSchema(t, schemas, "ImageConnectionPolicyPatch", []string{"mode", "value"}, []string{"mode"})
		assertValue(t, objectAt(t, schemas, "ImageConnectionPolicyPatch"), "additionalProperties", false)
		assertRef(t, propertyAt(t, schemas, "ImageConnectionPolicyPatch", "mode"), "#/components/schemas/PolicyPatchMode")
		assertEnum(t, propertyAt(t, schemas, "ImageConnectionPolicyPatch", "value"), []any{"fresh", "reuse"})
		assertKCEFPolicyPatchSchema(t, schemas)

		assertObjectSchema(t, schemas, "SourceTransportPolicyUpdate", []string{"reuseBypassSession", "imageConnectionMode", "kcefPolicy"}, nil)
		assertValue(t, objectAt(t, schemas, "SourceTransportPolicyUpdate"), "additionalProperties", false)
		assertRef(t, propertyAt(t, schemas, "SourceTransportPolicyUpdate", "reuseBypassSession"), "#/components/schemas/BooleanPolicyPatch")
		assertRef(t, propertyAt(t, schemas, "SourceTransportPolicyUpdate", "imageConnectionMode"), "#/components/schemas/ImageConnectionPolicyPatch")
		assertRef(t, propertyAt(t, schemas, "SourceTransportPolicyUpdate", "kcefPolicy"), "#/components/schemas/KCEFPolicyPatch")
		assertObjectSchema(t, schemas, "SourceImageProxyMembershipUpdate", []string{"enabled"}, []string{"enabled"})
		assertValue(t, objectAt(t, schemas, "SourceImageProxyMembershipUpdate"), "additionalProperties", false)
		assertType(t, propertyAt(t, schemas, "SourceImageProxyMembershipUpdate", "enabled"), "boolean")
		assertObjectSchema(t, schemas, "SourceNetworkBindingUpdate", []string{"socksEndpointId", "flareMode", "flareEndpointId"}, []string{"flareMode"})
		assertValue(t, objectAt(t, schemas, "SourceNetworkBindingUpdate"), "additionalProperties", false)
		assertEnum(t, propertyAt(t, schemas, "SourceNetworkBindingUpdate", "flareMode"), []any{"none", "global", "endpoint"})
		assertObjectSchema(t, schemas, "SourceMutationResponse", []string{"configuration", "runtime"}, []string{"configuration", "runtime"})
		assertRef(t, propertyAt(t, schemas, "SourceMutationResponse", "configuration"), "#/components/schemas/SourceEffectiveConfiguration")
		assertRef(t, propertyAt(t, schemas, "SourceMutationResponse", "runtime"), "#/components/schemas/SourceRuntimeStatus")
	})

	t.Run("routes", func(t *testing.T) {
		paths := objectAt(t, doc, "paths")
		assertArrayResponse(t, paths, "/api/sources/exceptions", "get", "200", "#/components/schemas/SourceExceptionSummary")
		assertResponseRef(t, paths, "/api/sources/{sourceId}/effective-configuration", "get", "200", "#/components/schemas/SourceEffectiveConfiguration")
		assertRequestRef(t, paths, "/api/sources/{sourceId}/transport", "patch", "#/components/schemas/SourceTransportPolicyUpdate")
		assertResponseRef(t, paths, "/api/sources/{sourceId}/transport", "patch", "200", "#/components/schemas/SourceMutationResponse")
		assertRequestRef(t, paths, "/api/sources/{sourceId}/image-proxy", "put", "#/components/schemas/SourceImageProxyMembershipUpdate")
		assertResponseRef(t, paths, "/api/sources/{sourceId}/image-proxy", "put", "200", "#/components/schemas/SourceMutationResponse")
		assertRequestRef(t, paths, "/api/network/bindings/{sourceId}", "put", "#/components/schemas/SourceNetworkBindingUpdate")
		assertResponseRef(t, paths, "/api/network/bindings/{sourceId}", "put", "200", "#/components/schemas/SourceMutationResponse")
		assertResponseRef(t, paths, "/api/network/bindings/{sourceId}", "put", "404", "#/components/schemas/ErrorResponse")
		assertResponseDescription(t, paths, "/api/network/bindings/{sourceId}", "put", "404", "Source not found.")
		assertResponseRef(t, paths, "/api/network/bindings/{sourceId}", "delete", "200", "#/components/schemas/SourceMutationResponse")
		for _, route := range []struct {
			path   string
			method string
		}{
			{"/api/sources/{sourceId}/transport", "patch"},
			{"/api/sources/{sourceId}/image-proxy", "put"},
			{"/api/network/bindings/{sourceId}", "put"},
			{"/api/network/bindings/{sourceId}", "delete"},
		} {
			t.Run(route.method+" "+route.path+" catalog unavailable", func(t *testing.T) {
				assertResponseRef(t, paths, route.path, route.method, "503", "#/components/schemas/ErrorResponse")
				assertResponseDescription(t, paths, route.path, route.method, "503", mutationCatalogUnavailableDescription)
			})
		}
		if _, exists := paths["/api/network/sources/{sourceId}/binding"]; exists {
			t.Fatal("legacy network binding path remains in the contract")
		}

		for _, route := range []struct {
			path   string
			method string
		}{
			{"/api/sources/{sourceId}/effective-configuration", "get"},
			{"/api/sources/{sourceId}/transport", "patch"},
			{"/api/sources/{sourceId}/image-proxy", "put"},
			{"/api/network/bindings/{sourceId}", "put"},
			{"/api/network/bindings/{sourceId}", "delete"},
		} {
			assertStringSourceIDParameter(t, paths, route.path, route.method)
		}

		for _, route := range []struct {
			path    string
			methods []string
		}{
			{path: "/api/sources/exceptions", methods: []string{"get"}},
			{path: "/api/sources/{sourceId}/effective-configuration", methods: []string{"get"}},
			{path: "/api/sources/{sourceId}/transport", methods: []string{"patch"}},
			{path: "/api/sources/{sourceId}/image-proxy", methods: []string{"put"}},
			{path: "/api/network/bindings/{sourceId}", methods: []string{"delete", "put"}},
		} {
			assertStringSet(t, keys(objectAt(t, paths, route.path)), route.methods, route.path+" methods")
			for _, method := range route.methods {
				assertBearerSecurity(t, paths, route.path, method)
			}
		}
	})
}

func decodeOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(Spec, &doc); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	return doc
}

func objectAt(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key]
	if !ok {
		t.Fatalf("missing %q", key)
	}
	child, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%q = %T, want object", key, value)
	}
	return child
}

func arrayAt(t *testing.T, object map[string]any, key string) []any {
	t.Helper()
	value, ok := object[key]
	if !ok {
		t.Fatalf("missing %q", key)
	}
	child, ok := value.([]any)
	if !ok {
		t.Fatalf("%q = %T, want array", key, value)
	}
	return child
}

func propertyAt(t *testing.T, schemas map[string]any, schemaName, propertyName string) map[string]any {
	t.Helper()
	schema := objectAt(t, schemas, schemaName)
	return objectAt(t, objectAt(t, schema, "properties"), propertyName)
}

func assertObjectSchema(t *testing.T, schemas map[string]any, name string, properties, required []string) {
	t.Helper()
	schema := objectAt(t, schemas, name)
	assertType(t, schema, "object")
	assertStringSet(t, keys(objectAt(t, schema, "properties")), properties, name+" properties")
	if required == nil {
		if value, ok := schema["required"]; ok {
			assertStringSet(t, stringsFromAny(t, value), nil, name+" required")
		}
		return
	}
	assertStringSet(t, stringsFromAny(t, schema["required"]), required, name+" required")
}

func assertPolicyValue(t *testing.T, schemas map[string]any, name string, overrideType []any, effectiveType string) {
	t.Helper()
	assertObjectSchema(t, schemas, name, []string{"override", "effective", "inherited"}, []string{"override", "effective", "inherited"})
	assertTypes(t, propertyAt(t, schemas, name, "override"), overrideType)
	assertType(t, propertyAt(t, schemas, name, "effective"), effectiveType)
	assertType(t, propertyAt(t, schemas, name, "inherited"), "boolean")
}

func assertKCEFPolicyPatchSchema(t *testing.T, schemas map[string]any) {
	t.Helper()
	variants := arrayAt(t, objectAt(t, schemas, "KCEFPolicyPatch"), "oneOf")
	if len(variants) != 2 {
		t.Fatalf("KCEFPolicyPatch variants = %d, want 2", len(variants))
	}
	inherit, ok := variants[0].(map[string]any)
	if !ok {
		t.Fatalf("inherit variant = %T, want object", variants[0])
	}
	assertType(t, inherit, "object")
	assertValue(t, inherit, "additionalProperties", false)
	assertStringSet(t, keys(objectAt(t, inherit, "properties")), []string{"mode"}, "inherit properties")
	assertStringSet(t, stringsFromAny(t, inherit["required"]), []string{"mode"}, "inherit required")
	assertValue(t, objectAt(t, objectAt(t, inherit, "properties"), "mode"), "const", "inherit")

	override, ok := variants[1].(map[string]any)
	if !ok {
		t.Fatalf("override variant = %T, want object", variants[1])
	}
	assertType(t, override, "object")
	assertValue(t, override, "additionalProperties", false)
	assertStringSet(t, keys(objectAt(t, override, "properties")), []string{"mode", "value"}, "override properties")
	assertStringSet(t, stringsFromAny(t, override["required"]), []string{"mode", "value"}, "override required")
	assertValue(t, objectAt(t, objectAt(t, override, "properties"), "mode"), "const", "override")
	assertEnum(t, objectAt(t, objectAt(t, override, "properties"), "value"), []any{"auto", "required", "disabled"})
}

func assertEnumSchema(t *testing.T, schemas map[string]any, name string, values []any) {
	t.Helper()
	schema := objectAt(t, schemas, name)
	assertType(t, schema, "string")
	assertEnum(t, schema, values)
}

func assertType(t *testing.T, schema map[string]any, want string) {
	t.Helper()
	assertValue(t, schema, "type", want)
}

func assertTypes(t *testing.T, schema map[string]any, want []any) {
	t.Helper()
	assertValue(t, schema, "type", want)
}

func assertTypeAndFormat(t *testing.T, schema map[string]any, wantType, wantFormat string) {
	t.Helper()
	assertType(t, schema, wantType)
	assertValue(t, schema, "format", wantFormat)
}

func assertEnum(t *testing.T, schema map[string]any, want []any) {
	t.Helper()
	assertValue(t, schema, "enum", want)
}

func assertRef(t *testing.T, schema map[string]any, want string) {
	t.Helper()
	assertValue(t, schema, "$ref", want)
}

func assertValue(t *testing.T, object map[string]any, key string, want any) {
	t.Helper()
	got, ok := object[key]
	if !ok {
		t.Fatalf("missing %q in %#v", key, object)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q = %#v, want %#v", key, got, want)
	}
}

func assertRequestRef(t *testing.T, paths map[string]any, path, method, want string) {
	t.Helper()
	operation := objectAt(t, objectAt(t, paths, path), method)
	requestBody := objectAt(t, operation, "requestBody")
	assertValue(t, requestBody, "required", true)
	content := objectAt(t, requestBody, "content")
	schema := objectAt(t, objectAt(t, content, "application/json"), "schema")
	assertRef(t, schema, want)
}

func assertResponseDescription(t *testing.T, paths map[string]any, path, method, status, want string) {
	t.Helper()
	operation := objectAt(t, objectAt(t, paths, path), method)
	response := objectAt(t, objectAt(t, operation, "responses"), status)
	assertValue(t, response, "description", want)
}

func assertResponseRef(t *testing.T, paths map[string]any, path, method, status, want string) {
	t.Helper()
	operation := objectAt(t, objectAt(t, paths, path), method)
	response := objectAt(t, objectAt(t, operation, "responses"), status)
	content := objectAt(t, response, "content")
	schema := objectAt(t, objectAt(t, content, "application/json"), "schema")
	assertRef(t, schema, want)
}

func assertArrayResponse(t *testing.T, paths map[string]any, path, method, status, itemRef string) {
	t.Helper()
	operation := objectAt(t, objectAt(t, paths, path), method)
	response := objectAt(t, objectAt(t, operation, "responses"), status)
	content := objectAt(t, response, "content")
	schema := objectAt(t, objectAt(t, content, "application/json"), "schema")
	assertType(t, schema, "array")
	assertRef(t, objectAt(t, schema, "items"), itemRef)
}

func assertStringSourceIDParameter(t *testing.T, paths map[string]any, path, method string) {
	t.Helper()
	operation := objectAt(t, objectAt(t, paths, path), method)
	for _, raw := range arrayAt(t, operation, "parameters") {
		parameter, ok := raw.(map[string]any)
		if !ok || parameter["name"] != "sourceId" || parameter["in"] != "path" {
			continue
		}
		assertValue(t, parameter, "required", true)
		schema := objectAt(t, parameter, "schema")
		assertType(t, schema, "string")
		assertValue(t, schema, "pattern", "^-?[0-9]+$")
		return
	}
	t.Fatalf("%s %s is missing its sourceId path parameter", method, path)
}

func assertBearerSecurity(t *testing.T, paths map[string]any, path, method string) {
	t.Helper()
	operation := objectAt(t, objectAt(t, paths, path), method)
	assertValue(t, operation, "security", []any{map[string]any{"BearerAuth": []any{}}})
}

func keys(object map[string]any) []string {
	result := make([]string, 0, len(object))
	for key := range object {
		result = append(result, key)
	}
	return result
}

func stringsFromAny(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%#v = %T, want string array", value, value)
	}
	result := make([]string, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("%#v[%d] = %T, want string", value, index, item)
		}
		result[index] = text
	}
	return result
}

func assertStringSet(t *testing.T, got, want []string, label string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}
