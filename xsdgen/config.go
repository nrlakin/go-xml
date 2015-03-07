package xsdgen

import (
	"encoding/xml"
	"fmt"
	"go/ast"
	"regexp"
	"strings"
	"unicode"

	"aqwari.net/xml/internal/gen"
	"aqwari.net/xml/xsd"
)

// A Config holds user-defined overrides and filters that are used when
// generating Go source code from an xsd document.
type Config struct {
	logger          Logger
	loglevel        int
	pkgname         string
	preprocessType  typeTransform
	postprocessType specTransform
	// Attributes for which this returns true won't be a part
	// of any complex types.
	filterAttributes propertyFilter
	// Elements for which this returns true won't be a part
	// of any complex types.
	filterElements propertyFilter
	// Types for which this returns true won't be declared in
	// the go source.
	filterTypes propertyFilter
	// Transform for names
	typeNameTransform nameTransform
	elemNameTransform nameTransform
	attrNameTransform nameTransform
	allNameTransform  nameTransform
}

type nameTransform func(xml.Name) string
type typeTransform func(xsd.Schema, xsd.Type) xsd.Type
type propertyFilter func(interface{}) bool
type specTransform func(spec) spec

func (cfg *Config) errorf(format string, v ...interface{}) {
	if cfg.logger != nil {
		cfg.logger.Printf(format, v...)
	}
}
func (cfg *Config) logf(format string, v ...interface{}) {
	if cfg.logger != nil && cfg.loglevel > 0 {
		cfg.logger.Printf(format, v...)
	}
}
func (cfg *Config) debugf(format string, v ...interface{}) {
	if cfg.logger != nil && cfg.loglevel > 3 {
		cfg.logger.Printf(format, v...)
	}
}

// An Option is used to customize a Config.
type Option func(*Config) Option

// DefaultOptions are the default options for Go source code generation.
// The defaults are chosen to be good enough for the majority of use
// cases, and produce usable, idiomatic Go code. The top-level Generate
// function of the xsdgen package uses these options.
var DefaultOptions = []Option{
	IgnoreAttributes("id", "href", "ref", "offset"),
	ReplaceAllNames(`[._ \s-]`, ""),
	PackageName("ws"),
	HandleSOAPArrayType(),
	SOAPArrayAsSlice(),
}

// The Option method is used to configure an existing configuration.
// The return value of the Option method can be used to revert the
// final option to its previous setting.
func (cfg *Config) Option(opts ...Option) (previous Option) {
	for _, opt := range opts {
		previous = opt(cfg)
	}
	return previous
}

// Types implementing the Logger interface can receive
// debug information from the code generation process.
// The Logger interface is implemented by *log.Logger.
type Logger interface {
	Printf(format string, v ...interface{})
}

// ErrorLog specifies an optional Logger for warnings and debug
// information about the code generation process. The level parameter
// should be a positive integer between 1 and 5, with 5 providing the
// greatest verbosity.
func ErrorLog(l Logger, level int) Option {
	return func(cfg *Config) Option {
		prevLogger := cfg.logger
		prevLevel := cfg.loglevel
		cfg.logger = l
		cfg.loglevel = level
		return ErrorLog(prevLogger, prevLevel)
	}
}

func replacePropertyFilter(p *propertyFilter, fn propertyFilter) Option {
	return func(*Config) Option {
		prev := *p
		*p = fn
		return replacePropertyFilter(p, prev)
	}
}

// IgnoreAttributes defines a list of attributes that should not be
// declared in the Go type.
func IgnoreAttributes(names ...string) Option {
	return func(cfg *Config) Option {
		return replacePropertyFilter(&cfg.filterAttributes, func(v interface{}) bool {
			attr, ok := v.(*xsd.Attribute)
			if !ok {
				panic(fmt.Sprintf("non-attribute %[1]T %[1]v passed to cfg.filterAttributes", v))
			}
			for _, match := range names {
				if attr.Name.Local == match {
					return true
				}
			}
			return false
		})(cfg)
	}
}

// IgnoreElements defines a list of elements that should not be declared
// in the Go type.
func IgnoreElements(names ...string) Option {
	return func(cfg *Config) Option {
		return replacePropertyFilter(&cfg.filterElements, func(v interface{}) bool {
			el, ok := v.(*xsd.Element)
			if !ok {
				panic(fmt.Sprintf("non-element %[1]T %[1]v passed to cfg.filterElements", v))
			}
			for _, match := range names {
				if el.Name.Local == match {
					return true
				}
			}
			return false
		})(cfg)
	}
}

// OnlyTypes defines a whitelist of fully-qualified type name patterns
// to include in the generated Go source. Only types in the whitelist,
// and types that they depend on, will be included in the Go source.
func OnlyTypes(patterns ...string) Option {
	pat := strings.Join(patterns, "|")
	reg, err := regexp.Compile(pat)

	return func(cfg *Config) Option {
		return replacePropertyFilter(&cfg.filterTypes, func(v interface{}) bool {
			t, ok := v.(xsd.Type)
			if !ok {
				panic(fmt.Sprintf("non-type %[1]T %[1]v passed to cfg.filterTypes", v))
			}
			if err != nil {
				cfg.logf("invalid regex %q passed to OnlyTypes: %v", pat, err)
				return false
			}
			return !reg.MatchString(xsd.XMLName(t).Local)
		})(cfg)
	}
}

func replaceNameTransform(p *nameTransform, fn nameTransform) Option {
	return func(*Config) Option {
		prev := *p
		*p = fn
		return replaceNameTransform(p, prev)
	}
}

func replacePreprocessType(p *typeTransform, fn typeTransform) Option {
	return func(*Config) Option {
		prev := *p
		*p = fn
		return replacePreprocessType(p, prev)
	}
}

func replacePostprocessType(p *specTransform, fn specTransform) Option {
	return func(*Config) Option {
		prev := *p
		*p = fn
		return replacePostprocessType(p, prev)
	}
}

// PackageName specifies the name of the generated Go
// package.
func PackageName(name string) Option {
	return func(cfg *Config) Option {
		prev := cfg.pkgname
		cfg.pkgname = name
		return PackageName(prev)
	}
}

// ReplaceAllNames allows for substitution rules for all identifiers to
// be specified. If an invalid regular expression is called, no action
// is taken. The ReplaceAllNames option is additive; subsitutions will be
// applied in the order that each option was applied in.
func ReplaceAllNames(pat, repl string) Option {
	reg, err := regexp.Compile(pat)

	return func(cfg *Config) Option {
		prev := cfg.allNameTransform
		return replaceNameTransform(&cfg.allNameTransform,
			func(name xml.Name) string {
				s := name.Local
				if prev != nil {
					s = prev(name)
				}
				if err != nil {
					cfg.logf("Invalid regex %q passed to ReplaceAllNames", pat)
					return s
				}
				return reg.ReplaceAllString(s, repl)
			})(cfg)
	}
}

func replaceAllNamesRegex(reg *regexp.Regexp, repl string) Option {
	return func(cfg *Config) Option {
		prev := cfg.allNameTransform
		return replaceNameTransform(&cfg.allNameTransform,
			func(name xml.Name) string {
				s := name.Local
				if prev != nil {
					s = prev(name)
				}
				return reg.ReplaceAllString(s, repl)
			})(cfg)
	}
}

// ProcessTypes allows for users to make arbitrary changes to a type before
// Go source code is generated.
func ProcessTypes(fn func(xsd.Schema, xsd.Type) xsd.Type) Option {
	return func(cfg *Config) Option {
		prev := cfg.preprocessType
		return replacePreprocessType(&cfg.preprocessType, func(s xsd.Schema, t xsd.Type) xsd.Type {
			if prev != nil {
				t = prev(s, t)
			}
			return fn(s, t)
		})(cfg)
	}
}

// The Option HandleSOAPArrayType adds a special-case pre-processing step to
// xsdgen that parses the wsdl:arrayType attribute of a SOAP array declaration
// and changes the underlying base type to match.
func HandleSOAPArrayType() Option {
	return func(cfg *Config) Option {
		prev := cfg.preprocessType
		return replacePreprocessType(&cfg.preprocessType, func(s xsd.Schema, t xsd.Type) xsd.Type {
			if prev != nil {
				t = prev(s, t)
			}
			return cfg.parseSOAPArrayType(s, t)
		})(cfg)
	}
}

// The Option SOAPArrayAsSlice converts complex types with a single, plural
// element to a slice of the element's type.
func SOAPArrayAsSlice() Option {
	return func(cfg *Config) Option {
		prev := cfg.postprocessType
		return replacePostprocessType(&cfg.postprocessType, func(s spec) spec {
			if prev != nil {
				s = prev(s)
			}
			return cfg.soapArrayToSlice(s)
		})(cfg)
	}
}

func (cfg *Config) filterFields(t *xsd.ComplexType) ([]xsd.Attribute, []xsd.Element) {
	var (
		elements   []xsd.Element
		attributes []xsd.Attribute
	)
	for _, attr := range t.Attributes {
		if cfg.filterAttributes != nil && cfg.filterAttributes(&attr) {
			continue
		}
		attributes = append(attributes, attr)
	}
	for _, el := range t.Elements {
		if cfg.filterElements != nil && cfg.filterElements(&el) {
			continue
		}
		elements = append(elements, el)
	}
	return attributes, elements
}

// Return the identifier for non-builtin types, and the Go expression
// mapped to the built-in type.
func (cfg *Config) expr(t xsd.Type) (ast.Expr, error) {
	if t, ok := t.(xsd.Builtin); ok {
		ex := builtinExpr(t)
		if ex == nil {
			return nil, fmt.Errorf("Unknown built-in type %q", t.Name().Local)
		}
		return ex, nil
	}
	return ast.NewIdent(cfg.typeName(xsd.XMLName(t))), nil
}

func (cfg *Config) typeName(name xml.Name) string {
	return cfg.public(name)
}

func (cfg *Config) public(name xml.Name) string {
	return strings.Title(cfg.private(name))
}

func (cfg *Config) private(name xml.Name) string {
	s := name.Local
	if cfg.allNameTransform != nil {
		s = cfg.allNameTransform(name)
	}
	r := []rune(s)
	if len(r) == 0 {
		if len(name.Local) > 0 {
			cfg.logf("Name %s transformed to the empty string", name.Local)
		}
		return "_"
	}
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// SOAP arrays are declared as follows (unimportant fields ellided):
//
// 	<xs:complexType name="Array">
// 	  <xs:attribute name="arrayType" type="xs:string" />
// 	  <xs:any namespace="##any" minOccurs="0" maxOccurs="unbounded" />
// 	</xs:complexType>
//
// Then schemas that want to declare a fixed-type soap array do so like this:
//
// 	<xs:complexType name="IntArray">
// 	  <xs:complexContent>
// 	    <xs:restriction base="soapenc:Array>
// 	      <xs:attribute ref="soapenc:arrayType" wsdl:arrayType="xs:int[]" />
// 	    </xs:restriction>
// 	  </xs:complexContent>
// 	</xs:complexType>
//
// XML Schema is wonderful, aint it?
func (cfg *Config) parseSOAPArrayType(s xsd.Schema, t xsd.Type) xsd.Type {
	const soapenc = "http://schemas.xmlsoap.org/soap/encoding/"
	const wsdl = "http://schemas.xmlsoap.org/wsdl/"
	var itemType xml.Name

	c, ok := t.(*xsd.ComplexType)
	if !ok {
		return t
	}
	for i, v := range c.Attributes {
		if v.Name.Local != "arrayType" {
			continue
		}
		for _, a := range v.Attr {
			if (a.Name != xml.Name{wsdl, "arrayType"}) {
				continue
			}
			itemType = v.Resolve(a.Value)
			c.Attributes[i].Fixed = a.Value
			break
		}
		break
	}
	if itemType.Local == "" {
		return c
	}
	itemType.Local = strings.TrimSpace(itemType.Local)
	itemType.Local = strings.TrimSuffix(itemType.Local, "[]")
	if b := s.FindType(itemType); b != nil {
		c = cfg.overrideWildcardType(c, b)
	} else {
		cfg.logf("could not lookup item type %q in namespace %q",
			itemType.Local, itemType.Space)
	}
	return c
}

func (cfg *Config) overrideWildcardType(t *xsd.ComplexType, base xsd.Type) *xsd.ComplexType {
	var elem xsd.Element
	var found bool
	var replaced bool
Loop:
	for x := xsd.Type(t); xsd.Base(x) != nil; x = xsd.Base(x) {
		c, ok := x.(*xsd.ComplexType)
		if !ok {
			cfg.logf("warning: soap-encoded array %s extends %T %s",
				xsd.XMLName(x).Local, base, xsd.XMLName(base).Local)
			return t
		}
		for _, v := range c.Elements {
			if v.Wildcard {
				elem = v
				found = true
				break Loop
			}
		}
	}
	if !found {
		cfg.logf("could not override wildcard type for %s; not found in type hierarchy", t.Name.Local)
		return t
	}
	cfg.debugf("overriding wildcard element of %s type from %s to %s",
		t.Name.Local, xsd.XMLName(elem.Type).Local, xsd.XMLName(base).Local)
	elem.Type = base
	for i, v := range t.Elements {
		if v.Wildcard {
			t.Elements[i] = elem
			replaced = true
		}
	}
	if !replaced {
		t.Elements = append(t.Elements, elem)
	}
	return t
}

// SOAP arrays (and other similar types) are complex types with a single
// plural element. We add a post-processing step to flatten it out and provide
// marshal/unmarshal methods.
func (cfg *Config) soapArrayToSlice(s spec) spec {
	str, ok := s.expr.(*ast.StructType)
	if !ok {
		return s
	}
	if len(str.Fields.List) != 1 {
		return s
	}
	slice, ok := str.Fields.List[0].Type.(*ast.ArrayType)
	if !ok {
		return s
	}
	cfg.debugf("flattening single-element slice struct type %s to []%v", s.name, slice.Elt)
	tag := gen.TagKey(str.Fields.List[0], "xml")
	xmltag := xml.Name{"", ",any"}

	if tag != "" {
		parts := strings.Split(tag, ",")
		if len(parts) > 0 {
			fields := strings.Fields(parts[0])
			if len(fields) > 0 {
				xmltag.Local = fields[len(fields)-1]
			}
			if len(fields) > 1 {
				xmltag.Space = fields[0]
			}
		}
	}

	itemType := gen.ExprString(slice.Elt)
	unmarshal, err := gen.Func("UnmarshalXML").
		Receiver("a *"+s.name).
		Args("d *xml.Decoder", "start xml.StartElement").
		Returns("err error").
		Body(`
			var tok xml.Token
			var itemTag = xml.Name{%q, %q}
			
			for tok, err = d.Token(); err == nil; tok, err = d.Token() {
				if tok, ok := tok.(xml.StartElement); ok {
					var item %s
					if itemTag.Local != ",any" && itemTag != tok.Name {
						err = d.Skip()
						continue
					}
					if err = d.DecodeElement(&item, &tok); err == nil {
						*a = append(*a, item)
					}
				}
				if _, ok := tok.(xml.EndElement); ok {
					break
				}
			}
			return err
		`, xmltag.Space, xmltag.Local, itemType).Decl()
	if err != nil {
		cfg.logf("error generating UnmarshalXML method of %s: %v", s.name, err)
		return s
	}

	marshal, err := gen.Func("MarshalXML").
		Receiver("a *"+s.name).
		Args("e *xml.Encoder", "start xml.StartElement").
		Returns("error").
		Body(`
			tag := xml.StartElement{Name: xml.Name{"", "item"}}
			for _, elt := range *a {
				if err := e.EncodeElement(elt, tag); err != nil {
					return err
				}
			}
			return nil
		`).Decl()
	if err != nil {
		cfg.logf("error generating MarshalXML method of %s: %v", s.name, err)
		return s
	}

	s.expr = slice
	s.methods = append(s.methods, marshal)
	s.methods = append(s.methods, unmarshal)
	return s
}
