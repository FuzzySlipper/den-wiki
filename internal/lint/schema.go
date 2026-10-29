package lint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/FuzzySlipper/den-wiki/schemas"
)

// schemaSet holds the compiled contract schemas.
type schemaSet struct {
	page, source, trail, taxonomy *jsonschema.Schema
}

func compileSchemas() (*schemaSet, error) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	load := func(name string) (*jsonschema.Schema, error) {
		raw, err := schemas.Files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		url := "file:///schemas/" + name
		if err := c.AddResource(url, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return c.Compile(url)
	}
	var s schemaSet
	var err error
	if s.page, err = load(schemas.WikiPage); err != nil {
		return nil, err
	}
	if s.source, err = load(schemas.Source); err != nil {
		return nil, err
	}
	if s.trail, err = load(schemas.Trail); err != nil {
		return nil, err
	}
	if s.taxonomy, err = load(schemas.Taxonomy); err != nil {
		return nil, err
	}
	return &s, nil
}

var printer = message.NewPrinter(language.English)

// validate returns one message per leaf schema error, as "<location>: <why>".
// fields is round-tripped through JSON so the validator sees plain JSON types.
func validate(sch *jsonschema.Schema, fields map[string]any) ([]string, error) {
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			out = append(out, loc+": "+e.ErrorKind.LocalizedString(printer))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	sort.Strings(out)
	return out, nil
}
