package cql2

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseJSON parses a CQL2-JSON filter document into the same AST as the text parser.
// The result can be passed to Translate() exactly like a Parse() result.
func ParseJSON(data []byte) (Expr, error) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("invalid CQL2-JSON: %w", err)
	}
	return parseJSONExpr(v)
}

func parseJSONExpr(v interface{}) (Expr, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected JSON object for expression, got %T", v)
	}

	opRaw, ok := m["op"]
	if !ok {
		return nil, fmt.Errorf("missing \"op\" field in expression")
	}
	op, ok := opRaw.(string)
	if !ok {
		return nil, fmt.Errorf("\"op\" must be a string")
	}

	argsRaw, ok := m["args"]
	if !ok {
		return nil, fmt.Errorf("missing \"args\" field for op %q", op)
	}
	rawArgs, ok := argsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("\"args\" must be an array for op %q", op)
	}

	switch strings.ToLower(op) {
	case "and", "or":
		if len(rawArgs) < 2 {
			return nil, fmt.Errorf("%q requires at least 2 arguments", op)
		}
		left, err := parseJSONExpr(rawArgs[0])
		if err != nil {
			return nil, err
		}
		logOp := strings.ToUpper(op)
		for _, argV := range rawArgs[1:] {
			right, err := parseJSONExpr(argV)
			if err != nil {
				return nil, err
			}
			left = &LogicalExpr{Op: logOp, Left: left, Right: right}
		}
		return left, nil

	case "not":
		if len(rawArgs) != 1 {
			return nil, fmt.Errorf("\"not\" requires exactly 1 argument")
		}
		inner, err := parseJSONExpr(rawArgs[0])
		if err != nil {
			return nil, err
		}
		return &NotExpr{Expr: inner}, nil

	case "eq", "neq", "lt", "gt", "lte", "gte":
		if len(rawArgs) != 2 {
			return nil, fmt.Errorf("%q requires exactly 2 arguments", op)
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("%q arg1: %w", op, err)
		}
		lit, err := parseJSONLiteral(rawArgs[1])
		if err != nil {
			return nil, fmt.Errorf("%q arg2: %w", op, err)
		}
		sqlOp := jsonOpToSQL(strings.ToLower(op))
		return &CompareExpr{Prop: prop, Op: sqlOp, Val: lit}, nil

	case "like":
		if len(rawArgs) != 2 {
			return nil, fmt.Errorf("\"like\" requires exactly 2 arguments")
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("\"like\" arg1: %w", err)
		}
		pat, ok := rawArgs[1].(string)
		if !ok {
			return nil, fmt.Errorf("\"like\" arg2: expected string pattern")
		}
		return &LikeExpr{Prop: prop, Pattern: pat}, nil

	case "isnull":
		if len(rawArgs) != 1 {
			return nil, fmt.Errorf("\"isNull\" requires exactly 1 argument")
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("\"isNull\" arg1: %w", err)
		}
		return &IsNullExpr{Prop: prop}, nil

	case "between":
		if len(rawArgs) != 3 {
			return nil, fmt.Errorf("\"between\" requires exactly 3 arguments")
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("\"between\" arg1: %w", err)
		}
		low, err := parseJSONLiteral(rawArgs[1])
		if err != nil {
			return nil, fmt.Errorf("\"between\" arg2: %w", err)
		}
		high, err := parseJSONLiteral(rawArgs[2])
		if err != nil {
			return nil, fmt.Errorf("\"between\" arg3: %w", err)
		}
		return &BetweenExpr{Prop: prop, Low: low, High: high}, nil

	case "in":
		if len(rawArgs) < 2 {
			return nil, fmt.Errorf("\"in\" requires at least 2 arguments")
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("\"in\" arg1: %w", err)
		}
		// arg2 may be an array of values or args[1:] are individual values
		var vals []Literal
		if arr, ok := rawArgs[1].([]interface{}); ok {
			for i, v := range arr {
				lit, err := parseJSONLiteral(v)
				if err != nil {
					return nil, fmt.Errorf("\"in\" value[%d]: %w", i, err)
				}
				vals = append(vals, lit)
			}
		} else {
			for i, v := range rawArgs[1:] {
				lit, err := parseJSONLiteral(v)
				if err != nil {
					return nil, fmt.Errorf("\"in\" value[%d]: %w", i, err)
				}
				vals = append(vals, lit)
			}
		}
		return &InExpr{Prop: prop, Vals: vals}, nil

	case "s_intersects", "s_within", "s_contains", "s_disjoint",
		"s_overlaps", "s_touches", "s_crosses", "s_equals":
		if len(rawArgs) != 2 {
			return nil, fmt.Errorf("%q requires exactly 2 arguments", op)
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("%q arg1: %w", op, err)
		}
		geomSQL, err := parseJSONGeometry(rawArgs[1])
		if err != nil {
			return nil, fmt.Errorf("%q arg2: %w", op, err)
		}
		return &SpatialExpr{Op: strings.ToUpper(op), Prop: prop, GeomSQL: geomSQL}, nil

	case "t_after", "t_before", "t_during", "t_equals":
		if len(rawArgs) != 2 {
			return nil, fmt.Errorf("%q requires exactly 2 arguments", op)
		}
		prop, err := parseJSONPropertyRef(rawArgs[0])
		if err != nil {
			return nil, fmt.Errorf("%q arg1: %w", op, err)
		}
		return parseJSONTemporalExpr(strings.ToUpper(op), prop, rawArgs[1])

	default:
		return nil, fmt.Errorf("unsupported CQL2-JSON op %q", op)
	}
}

// parseJSONPropertyRef extracts a property name from {"property": "name"}.
func parseJSONPropertyRef(v interface{}) (string, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("expected property reference object {\"property\":\"name\"}, got %T", v)
	}
	propRaw, ok := m["property"]
	if !ok {
		return "", fmt.Errorf("expected {\"property\":\"name\"}")
	}
	name, ok := propRaw.(string)
	if !ok {
		return "", fmt.Errorf("property name must be a string")
	}
	return name, nil
}

func parseJSONLiteral(v interface{}) (Literal, error) {
	switch val := v.(type) {
	case string:
		return Literal{Kind: "string", Str: val}, nil
	case float64:
		return Literal{Kind: "number", Num: val}, nil
	case bool:
		return Literal{Kind: "bool", Bool: val}, nil
	case nil:
		return Literal{Kind: "null"}, nil
	default:
		return Literal{}, fmt.Errorf("unsupported literal type %T", v)
	}
}

// parseJSONGeometry converts a CQL2-JSON geometry argument to a DuckDB SQL fragment.
// Supports {"bbox": [minx, miny, maxx, maxy]} and GeoJSON geometry objects.
func parseJSONGeometry(v interface{}) (string, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("expected geometry object, got %T", v)
	}

	// {"bbox": [minx, miny, maxx, maxy]}
	if bboxRaw, ok := m["bbox"]; ok {
		bbox, ok := bboxRaw.([]interface{})
		if !ok || len(bbox) != 4 {
			return "", fmt.Errorf("bbox must be an array of exactly 4 numbers")
		}
		coords := make([]string, 4)
		for i, c := range bbox {
			f, ok := c.(float64)
			if !ok {
				return "", fmt.Errorf("bbox coordinate must be a number")
			}
			coords[i] = strconv.FormatFloat(f, 'f', -1, 64)
		}
		return fmt.Sprintf("ST_MakeEnvelope(%s, %s, %s, %s)",
			coords[0], coords[1], coords[2], coords[3]), nil
	}

	// GeoJSON geometry: use ST_GeomFromGeoJSON
	typeRaw, ok := m["type"]
	if !ok {
		return "", fmt.Errorf("geometry object must have \"type\" or \"bbox\"")
	}
	geomType, ok := typeRaw.(string)
	if !ok {
		return "", fmt.Errorf("geometry \"type\" must be a string")
	}
	switch geomType {
	case "Point", "LineString", "Polygon",
		"MultiPoint", "MultiLineString", "MultiPolygon", "GeometryCollection":
		// valid
	default:
		return "", fmt.Errorf("unknown GeoJSON geometry type %q", geomType)
	}
	geojsonBytes, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to serialize geometry: %w", err)
	}
	escaped := strings.ReplaceAll(string(geojsonBytes), "'", "''")
	return fmt.Sprintf("ST_GeomFromGeoJSON('%s')", escaped), nil
}

// parseJSONTemporalExpr converts a CQL2-JSON temporal literal to a TemporalExpr.
func parseJSONTemporalExpr(op, prop string, v interface{}) (Expr, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected temporal literal object, got %T", v)
	}

	// {"timestamp": "2024-01-01T00:00:00Z"}
	if tsRaw, ok := m["timestamp"]; ok {
		ts, ok := tsRaw.(string)
		if !ok {
			return nil, fmt.Errorf("timestamp value must be a string")
		}
		return &TemporalExpr{Op: op, Prop: prop, Low: ts}, nil
	}

	// {"interval": ["start", "end"]}
	if ivRaw, ok := m["interval"]; ok {
		iv, ok := ivRaw.([]interface{})
		if !ok || len(iv) != 2 {
			return nil, fmt.Errorf("interval must be an array of exactly 2 timestamps")
		}
		if op != "T_DURING" {
			return nil, fmt.Errorf("interval literal is only valid for T_DURING, not %s", op)
		}
		low, ok := iv[0].(string)
		if !ok {
			return nil, fmt.Errorf("interval start must be a string")
		}
		high, ok := iv[1].(string)
		if !ok {
			return nil, fmt.Errorf("interval end must be a string")
		}
		return &TemporalExpr{Op: op, Prop: prop, Low: low, High: high}, nil
	}

	return nil, fmt.Errorf("temporal literal must have \"timestamp\" or \"interval\" key")
}

func jsonOpToSQL(op string) string {
	switch op {
	case "eq":
		return "="
	case "neq":
		return "<>"
	case "lt":
		return "<"
	case "gt":
		return ">"
	case "lte":
		return "<="
	case "gte":
		return ">="
	default:
		return "="
	}
}
