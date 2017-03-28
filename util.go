package gear

import (
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

// Compose composes a array of middlewares to one middleware
func Compose(mds ...Middleware) Middleware {
	switch len(mds) {
	case 0:
		return noOp
	case 1:
		return mds[0]
	default:
		return middlewares(mds).run
	}
}

var noOp Middleware = func(ctx *Context) error { return nil }

// WrapHandler wrap a http.Handler to Gear Middleware
func WrapHandler(handler http.Handler) Middleware {
	return func(ctx *Context) error {
		handler.ServeHTTP(ctx.Res, ctx.Req)
		return nil
	}
}

// WrapHandlerFunc wrap a http.HandlerFunc to Gear Middleware
func WrapHandlerFunc(fn http.HandlerFunc) Middleware {
	return func(ctx *Context) error {
		fn(ctx.Res, ctx.Req)
		return nil
	}
}

// IsNil checks if a specified object is nil or not, without Failing.
func IsNil(val interface{}) bool {
	if val == nil {
		return true
	}

	value := reflect.ValueOf(val)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Ptr, reflect.Interface, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// NewAppError create a error instance with "Gear: " prefix.
func NewAppError(err string) error {
	return fmt.Errorf("Gear: %s", err)
}

// ParseError parse a error, textproto.Error or HTTPError to HTTPError
func ParseError(e error, code ...int) HTTPError {
	if IsNil(e) {
		return nil
	}

	switch v := e.(type) {
	case HTTPError:
		return v
	case *textproto.Error:
		return &Error{v.Code, v.Msg, nil, ""}
	default:
		err := &Error{500, e.Error(), nil, ""}
		if len(code) > 0 && code[0] > 0 {
			err.Code = code[0]
		}
		return err
	}
}

// ErrorWithStack create a error with stacktrace
func ErrorWithStack(val interface{}, skip ...int) *Error {
	var err *Error
	if IsNil(val) {
		return err
	}

	switch v := val.(type) {
	case *Error:
		err = v
	case error:
		e := ParseError(v)
		err = &Error{e.Status(), e.Error(), nil, ""}
	case string:
		err = &Error{500, v, nil, ""}
	default:
		err = &Error{500, fmt.Sprintf("%#v", v), nil, ""}
	}

	if err.Stack == "" {
		buf := make([]byte, 2048)
		buf = buf[:runtime.Stack(buf, false)]
		s := 1
		if len(skip) != 0 {
			s = skip[0]
		}
		err.Stack = pruneStack(buf, s)
	}
	return err
}

// FormToStruct converts form values into struct object.
func FormToStruct(form map[string][]string, target interface{}) (err error) {
	if form == nil {
		return fmt.Errorf("invalid form value: %#v", form)
	}
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("invalid struct value: %#v", target)
	}

	structValue := rv.Elem()
	structType := reflect.TypeOf(target).Elem()

	for i := 0; i < structType.NumField(); i++ {
		fieldType := structType.Field(i)
		fieldValue := structValue.Field(i)
		if !fieldValue.CanSet() {
			continue
		}
		fieldKey := fieldType.Tag.Get("form")
		if fieldKey == "" {
			continue
		}

		if formValue, ok := form[fieldKey]; ok {
			if fieldValue.Kind() == reflect.Slice {
				err = setRefSlice(fieldValue, formValue)
			} else if len(formValue) > 0 {
				err = setRefField(fieldValue.Kind(), fieldValue, formValue[0])
			}
			if err != nil {
				return
			}
		}
	}

	return
}

func setRefSlice(field reflect.Value, value []string) error {
	lenValue := len(value)
	sliceKind := field.Type().Elem().Kind()
	slice := reflect.MakeSlice(field.Type(), lenValue, lenValue)

	for i := 0; i < lenValue; i++ {
		if err := setRefField(sliceKind, slice.Index(i), value[i]); err != nil {
			return err
		}
	}

	field.Set(slice)
	return nil
}

func setRefField(fieldKind reflect.Kind, field reflect.Value, value string) error {
	switch fieldKind {
	case reflect.String:
		field.SetString(value)
		return nil
	case reflect.Bool:
		return setRefBool(field, value)
	case reflect.Int:
		return setRefInt(field, value, 0)
	case reflect.Int8:
		return setRefInt(field, value, 8)
	case reflect.Int16:
		return setRefInt(field, value, 16)
	case reflect.Int32:
		return setRefInt(field, value, 32)
	case reflect.Int64:
		return setRefInt(field, value, 64)
	case reflect.Uint:
		return setRefUint(field, value, 0)
	case reflect.Uint8:
		return setRefUint(field, value, 8)
	case reflect.Uint16:
		return setRefUint(field, value, 16)
	case reflect.Uint32:
		return setRefUint(field, value, 32)
	case reflect.Uint64:
		return setRefUint(field, value, 64)
	case reflect.Float32:
		return setRefFloat(field, value, 32)
	case reflect.Float64:
		return setRefFloat(field, value, 64)
	}
	return fmt.Errorf("unknown field type: %#v", fieldKind)
}

func setRefBool(field reflect.Value, value string) error {
	val, err := strconv.ParseBool(value)
	if err == nil {
		field.SetBool(val)
	}
	return err
}

func setRefInt(field reflect.Value, value string, size int) error {
	val, err := strconv.ParseInt(value, 10, size)
	if err == nil {
		field.SetInt(val)
	}
	return err
}

func setRefUint(field reflect.Value, value string, size int) error {
	val, err := strconv.ParseUint(value, 10, size)
	if err == nil {
		field.SetUint(val)
	}
	return err
}

func setRefFloat(field reflect.Value, value string, size int) error {
	val, err := strconv.ParseFloat(value, size)
	if err == nil {
		field.SetFloat(val)
	}
	return err
}

// pruneStack make a thin conversion for stack information
// limit the count of lines to 5
// src:
// ```
// goroutine 9 [running]:
// runtime/debug.Stack(0x6, 0x6, 0xc42003c898)
//     /usr/local/Cellar/go/1.7.4_2/libexec/src/runtime/debug/stack.go:24 +0x79
// github.com/teambition/gear/logging.(*Logger).OutputWithStack(0xc420012a50, 0xed0092215, 0x573fdbb, 0x471f20, 0x0, 0xc42000dc1a, 0x6, 0xc42000dc01, 0xc42000dca0)
//     /Users/xus/go/src/github.com/teambition/gear/logging/logger.go:267 +0x4e
// github.com/teambition/gear/logging.(*Logger).Emerg(0xc420012a50, 0x2a9cc0, 0xc42000dca0)
//     /Users/xus/go/src/github.com/teambition/gear/logging/logger.go:171 +0xd3
// github.com/teambition/gear/logging.TestGearLogger.func2(0xc420018600)
//     /Users/xus/go/src/github.com/teambition/gear/logging/logger_test.go:90 +0x3c1
// testing.tRunner(0xc420018600, 0x33d240)
//     /usr/local/Cellar/go/1.7.4_2/libexec/src/testing/testing.go:610 +0x81
// created by testing.(*T).Run
//     /usr/local/Cellar/go/1.7.4_2/libexec/src/testing/testing.go:646 +0x2ec
// ```
// dst:
// ```
// Stack:
//     /usr/local/Cellar/go/1.7.4_2/libexec/src/runtime/debug/stack.go:24
//     /Users/xus/go/src/github.com/teambition/gear/logging/logger.go:283
//     /Users/xus/go/src/github.com/teambition/gear/logging/logger.go:171
//     /Users/xus/go/src/github.com/teambition/gear/logging/logger_test.go:90
//     /usr/local/Cellar/go/1.7.4_2/libexec/src/testing/testing.go:610
//     /usr/local/Cellar/go/1.7.4_2/libexec/src/testing/testing.go:646
// ```
func pruneStack(stack []byte, skip int) string {
	// remove first line
	// `goroutine 1 [running]:`
	lines := strings.Split(string(stack), "\n")[1:]
	newLines := make([]string, 0, len(lines)/2)

	num := 0
	for idx, line := range lines {
		if idx%2 == 0 {
			continue
		}
		skip--
		if skip >= 0 {
			continue
		}
		num++

		loc := strings.Split(line, " ")[0]
		loc = strings.Replace(loc, "\t", "\\t", -1)
		// only need odd line
		newLines = append(newLines, loc)
		if num == 10 {
			break
		}
	}
	return strings.Join(newLines, "\\n")
}

type atomicBool int32

func (b *atomicBool) isTrue() bool {
	return atomic.LoadInt32((*int32)(b)) == 1
}

func (b *atomicBool) swapTrue() bool {
	return atomic.SwapInt32((*int32)(b), 1) == 0
}

func (b *atomicBool) setTrue() {
	atomic.StoreInt32((*int32)(b), 1)
}

// IsStatusCode returns true if status is HTTP status code.
// https://en.wikipedia.org/wiki/List_of_HTTP_status_codes
func IsStatusCode(status int) bool {
	switch status {
	case 100, 101, 102,
		200, 201, 202, 203, 204, 205, 206, 207, 208, 226,
		300, 301, 302, 303, 304, 305, 306, 307, 308,
		400, 401, 402, 403, 404, 405, 406, 407, 408, 409, 410, 411, 412, 413, 414, 415, 416, 417, 418,
		421, 422, 423, 424, 426, 428, 429, 431, 440, 444, 449, 450, 451, 494, 495, 496, 497, 498, 499,
		500, 501, 502, 503, 504, 505, 506, 507, 508, 509, 510, 511, 520, 521, 522, 523, 524, 525, 526, 527:
		return true
	default:
		return false
	}
}

func isRedirectStatus(status int) bool {
	switch status {
	case 300, 301, 302, 303, 305, 307, 308:
		return true
	default:
		return false
	}
}

func isEmptyStatus(status int) bool {
	switch status {
	case 204, 205, 304:
		return true
	default:
		return false
	}
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// ContentDisposition implements a simple version of https://tools.ietf.org/html/rfc2183
// Use mime.ParseMediaType to parse Content-Disposition header.
func ContentDisposition(fileName, dispositionType string) (header string) {
	if dispositionType == "" {
		dispositionType = "attachment"
	}
	if fileName == "" {
		return dispositionType
	}

	header = fmt.Sprintf(`%s; filename="%s"`, dispositionType, quoteEscaper.Replace(fileName))
	fallbackName := url.PathEscape(fileName)
	if len(fallbackName) != len(fileName) {
		header = fmt.Sprintf(`%s; filename*=UTF-8''%s`, header, fallbackName)
	}
	return
}