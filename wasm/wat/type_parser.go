package wat

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/wasm"
)

// typeParsingState is used to give an appropriate typeParser.errorContext
type typeParsingState byte

const (
	parsingTypeUse typeParsingState = iota
	parsingType
	parsingParamOrResult
	parsingParam
	parsingResult
	parsingComplete
)

// typeParser parses an inlined type from a field such as "type" or "func" and dispatches to onTypeEnd.
//
// Ex. `(import "Math" "PI" (func $math.pi (result f32))`
//                           starts here --^           ^
//                            onTypeEnd resumes here --+
//
// Ex. `(type (func (param i32) (result i64)))`
//    starts here --^                       ^
//                 onTypeEnd resumes here --+
//
// Ex. `(module (import "" "" (func $main)))`
//                calls onTypeEnd here --^
//
// Note: Unlike normal parsers, this is not used for an entire field (enclosed by parens). Rather, this only handles
// "param" and "result" inner fields in the correct order.
// Note: typeParser is reusable. The caller resets when reaching the appropriate tokenRParen via reset.
type typeParser struct {
	// m is used as a function pointer to moduleParser.tokenParser. This updates based on state changes.
	m *moduleParser

	// onTypeEnd is invoked when the grammar "(param)* (result)?" completes.
	//
	// Note: this is called when neither a "param" nor a "result" field are found, or on any field following a "param"
	// that is not a "result".
	onTypeEnd tokenParser

	// state is initially parsingParamOrResult and updated alongside tokenParser
	state typeParsingState

	// inlinedTypes are a collection of types currently known to be inlined.
	// Ex. `(param i32)` in `(import (func (param i32)))`
	//
	// Note: The Text Format requires imports first, not types first. This resolution has to be done later. The impact
	// of this is types here can be removed if later discovered to be an explicitly defined type.
	//
	// For example, here the `(param i32)` type is initially considered inlined until the module type with the same
	// signature is read later: (module (import (func (param i32))) (type (func (param i32))))`
	inlinedTypes []*typeFunc

	// currentTypeIndex is set when there's a "type" field in a type use
	// See https://www.w3.org/TR/wasm-core-1/#type-uses%E2%91%A0
	currentTypeIndex *index

	// currentParams allow us to accumulate typeFunc.params across multiple fields, as well support abbreviated
	// anonymous parameters. ex. both (param i32) (param i32) and (param i32 i32) formats.
	// See https://www.w3.org/TR/wasm-core-1/#abbreviations%E2%91%A2
	currentParams []wasm.ValueType

	// currentParamIndex is used to give an appropriate errorContext
	currentParamIndex uint32

	// foundParam allows us to check if we found a type in a "param" field. We can't use currentParamIndex because when
	// parameters are abbreviated, ex. (param i32 i32), the currentParamIndex will be less than the type count.
	foundParam bool

	// currentResult is zero until set, and only set once as WebAssembly 1.0 only supports up to one result.
	currentResult wasm.ValueType
}

// beginTypeUse sets the next parser to beginTypeParamOrResult. reset must be called prior to this.
// This should only be called when reaching the first tokenLParen after the optional field name (tokenID).
//
// Ex. Given the source `(module (import (func $main (param i32))))`
//              beginTypeParamOrResult starts here --^          ^
//                                     onTypeEnd resumes here --+
//
// The onTypeEnd parameter is invoked once any "param" and "result" fields have been consumed.
//
// NOTE: An empty function is valid and will not reach a tokenLParen! Ex. `(module (import (func)))`
func (p *typeParser) beginTypeUse(onTypeEnd tokenParser) {
	p.onTypeEnd = onTypeEnd
	p.state = parsingTypeUse
	p.m.tokenParser = p.beginTypeParamOrResult
}

// beginTypeParamOrResult is a tokenParser called after a tokenLParen and accepts a "type", "param" or a "result" field
// (tokenKeyword).
func (p *typeParser) beginTypeParamOrResult(tok tokenType, tokenBytes []byte, line, col uint32) error {
	if tok == tokenKeyword && string(tokenBytes) == "type" {
		p.state = parsingType
		p.m.tokenParser = p.m.indexParser.beginParsingIndex(p.parseTypeIndexEnd)
		return nil
	}
	return p.beginParamOrResult(tok, tokenBytes, line, col)
}

// beginType sets the next parser to parseMoreParamsOrResult. reset must be called prior to this.
//
// Ex. Given the source `(module (type (func (param i32))))`
//        parsingParamOrResult starts here --^          ^
//                             onTypeEnd resumes here --+
//
// The onTypeEnd parameter is invoked once any "param" and "result" fields have been consumed.
//
// NOTE: An empty function is valid and will not reach a tokenLParen! Ex. `(module (type (func)))`
func (p *typeParser) beginType(onTypeEnd tokenParser) {
	p.onTypeEnd = onTypeEnd
	p.state = parsingParamOrResult
	p.m.tokenParser = p.beginParamOrResult
}

func (p *typeParser) reset() {
	p.currentTypeIndex = nil
	p.currentParams = nil
	p.currentParamIndex = 0
	p.currentResult = 0
}

func (p *typeParser) parseTypeIndexEnd(index *index) {
	p.currentTypeIndex = index
	p.state = parsingParamOrResult // because a type field can be followed by its signature
	p.m.tokenParser = p.parseMoreParamsOrResult
}

// beginParamOrResult is a tokenParser called after a tokenLParen and accepts either a "param" or a "result" field
// (tokenKeyword).
func (p *typeParser) beginParamOrResult(tok tokenType, tokenBytes []byte, line, col uint32) error {
	if tok == tokenKeyword {
		switch string(tokenBytes) {
		case "param":
			p.state = parsingParam
			p.foundParam = false
			p.m.tokenParser = p.parseParam
		case "result":
			p.state = parsingResult
			p.m.tokenParser = p.parseResult
		case "type": // cannot repeat
			return errors.New("redundant type")
		}
		return nil
	}
	// If we reach here, it is a syntax error, so punt it to onTypeEnd. Ex. (func ($param i32))
	return p.onTypeEnd(tok, tokenBytes, line, col)
}

func (p *typeParser) parseMoreParamsOrResult(tok tokenType, tokenBytes []byte, line, col uint32) error {
	if tok == tokenLParen {
		p.state = parsingParamOrResult
		p.m.tokenParser = p.beginParamOrResult
		return nil
	}
	// If we reached this point, we have one or more parameters, but no result. Ex. (func (param i32)) or (func)
	return p.onTypeEnd(tok, tokenBytes, line, col)
}

func (p *typeParser) parseParam(tok tokenType, tokenBytes []byte, _, _ uint32) error {
	switch tok {
	case tokenID: // Ex. $len
		return errors.New("TODO param name ex (param $len i32), but not in abbreviation ex (param $x i32 $y i32)")
	case tokenKeyword: // Ex. i32
		vt, err := parseValueType(tokenBytes)
		if err != nil {
			return err
		}
		p.currentParams = append(p.currentParams, vt)
		p.foundParam = true
	case tokenRParen: // end of this field
		if !p.foundParam {
			return errors.New("expected a type")
		}

		// since multiple param fields are valid, ex `(func (param i32) (param i64))`, prepare for any next.
		p.currentParamIndex++
		p.state = parsingParamOrResult
		p.m.tokenParser = p.parseMoreParamsOrResult
	default:
		return unexpectedToken(tok, tokenBytes)
	}
	return nil
}

// parseResult is a tokenParser inside a "result" field (tokenKeyword). When this field completes (tokenRParen), control
// transitions to parseComplete.
func (p *typeParser) parseResult(tok tokenType, tokenBytes []byte, _, _ uint32) error {
	switch tok {
	case tokenKeyword: // Ex. i32
		if p.currentResult != 0 {
			return errors.New("redundant type")
		}
		vt, err := parseValueType(tokenBytes)
		if err != nil {
			return err
		}
		p.currentResult = vt
	case tokenRParen: // end of this field
		if p.currentResult == 0 {
			return errors.New("expected a type")
		}
		p.m.tokenParser = p.onTypeEnd
		p.state = parsingComplete
	default:
		return unexpectedToken(tok, tokenBytes)
	}
	return nil
}

func (p *typeParser) errorContext() string {
	switch p.state {
	case parsingParam:
		return fmt.Sprintf(".param[%d]", p.currentParamIndex)
	case parsingResult:
		return ".result"
	case parsingType:
		return ".type"
	}
	return ""
}

var typeFuncEmpty = &typeFunc{}

// getTypeUse finalizes any current params or result and returns the current typeIndex and/or type
func (p *typeParser) getTypeUse() (typeIndex *index, sig *typeFunc) {
	typeIndex = p.currentTypeIndex

	// Don't conflate lack of verification type with nullary
	if typeIndex != nil && p.currentEqualsType(typeFuncEmpty) {
		return
	}

	// Search for an existing signature that matches the current type in the module types.
	for _, t := range p.m.module.typeFuncs {
		if p.currentEqualsType(t) {
			sig = t
			return
		}
	}

	// Search for an existing signature that matches the current type in the pending inlined types
	for _, t := range p.inlinedTypes {
		if p.currentEqualsType(t) {
			sig = t
			return
		}
	}

	sig = &typeFunc{"", p.currentParams, p.currentResult}

	// If we didn't find a match, we need to insert an inlined type to use it. We don't do this when there is a type
	// index because in this case, the signature is only used for verification on an existing type.
	if typeIndex == nil {
		p.inlinedTypes = append(p.inlinedTypes, sig)
	}
	return
}

// getType finalizes any current params or result and returns the current type.
//
// If the current type is in typeParser.inlinedTypes, it is removed prior to returning.
func (p *typeParser) getType(typeName string) (sig *typeFunc) {
	// Search inlined types in case a matching type was found after its type use.
	for i, t := range p.inlinedTypes {
		if p.currentEqualsType(t) {
			// If we got here, we found a type field after a type use. This means it wasn't an inlined type, rather an
			// out-of-order type. Hence, remove it from the inlined types and add it to the module types.
			p.inlinedTypes = append(p.inlinedTypes[:i], p.inlinedTypes[i+1:]...)
			sig = t
			break
		}
	}

	// While inlined types are supposed to re-use an existing type index, there's no no unique constraint on explicitly
	// defined module types. This means a duplicate type is not a bug: we don't check module.typeFuncs first.
	if sig == nil {
		sig = &typeFunc{typeName, p.currentParams, p.currentResult}
	}
	return
}

func (p *typeParser) currentEqualsType(t *typeFunc) bool {
	return bytes.Equal(p.currentParams, t.params) && p.currentResult == t.result
}

func parseValueType(tokenBytes []byte) (wasm.ValueType, error) {
	t := string(tokenBytes)
	switch t {
	case "i32":
		return wasm.ValueTypeI32, nil
	case "i64":
		return wasm.ValueTypeI64, nil
	case "f32":
		return wasm.ValueTypeF32, nil
	case "f64":
		return wasm.ValueTypeF64, nil
	default:
		return 0, fmt.Errorf("unknown type: %s", t)
	}
}