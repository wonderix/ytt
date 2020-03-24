package yttlibrary

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/k14s/ytt/pkg/structmeta"
	"github.com/k14s/ytt/pkg/yamlmeta"
	"github.com/k14s/ytt/pkg/yamltemplate"
)

const (
	AnnotationDataValues structmeta.AnnotationName = "data/values"
)

type DataValues struct {
	DocSet    *yamlmeta.DocumentSet
	MetasOpts yamltemplate.MetasOpts
}

func (v DataValues) Extract() ([]*yamlmeta.Document, []*yamlmeta.Document, []*yamlmeta.Document, error) {
	err := v.checkNonDocs(v.DocSet)
	if err != nil {
		return nil, nil, nil, err
	}

	valuesDocs, libraryValueDocs, nonValuesDocs, err := v.extract(v.DocSet)
	if err != nil {
		return nil, nil, nil, err
	}

	return valuesDocs, libraryValueDocs, nonValuesDocs, nil
}

func (v DataValues) extract(docSet *yamlmeta.DocumentSet) ([]*yamlmeta.Document, []*yamlmeta.Document, []*yamlmeta.Document, error) {
	var valuesDocs []*yamlmeta.Document
	var nonValuesDocs []*yamlmeta.Document
	var libraryValueDocs []*yamlmeta.Document

	for _, doc := range docSet.Items {
		var hasMatchingAnn bool
		var annArgs map[string]string

		for _, meta := range doc.GetMetas() {
			// TODO potentially use template.NewAnnotations(doc).Has(yttoverlay.AnnotationMatch)
			// however if doc was not processed by the template, it wont have any annotations set
			structMeta, err := yamltemplate.NewStructMetaFromMeta(meta, v.MetasOpts)
			if err != nil {
				return nil, nil, nil, err
			}
			for _, ann := range structMeta.Annotations {
				if ann.Name == AnnotationDataValues {
					if hasMatchingAnn {
						return nil, nil, nil, fmt.Errorf("%s annotation may only be used once per YAML doc", AnnotationDataValues)
					}
					hasMatchingAnn = true
					annArgs, err = processDVAnnotationArgs(ann.Content)
					if err != nil {
						return nil, nil, nil, err
					}
				}
			}
		}

		if hasMatchingAnn {
			if _, ok := annArgs["library"]; ok {
				libraryValueDocs = append(libraryValueDocs, doc)
			} else {
				valuesDocs = append(valuesDocs, doc)
			}
		} else {
			nonValuesDocs = append(nonValuesDocs, doc)
		}
	}

	return valuesDocs, libraryValueDocs, nonValuesDocs, nil
}

func (v DataValues) checkNonDocs(val interface{}) error {
	node, ok := val.(yamlmeta.Node)
	if !ok {
		return nil
	}

	for _, meta := range node.GetMetas() {
		structMeta, err := yamltemplate.NewStructMetaFromMeta(meta, v.MetasOpts)
		if err != nil {
			return err
		}

		for _, ann := range structMeta.Annotations {
			if ann.Name == AnnotationDataValues {
				// TODO check for annotation emptiness
				_, isDoc := node.(*yamlmeta.Document)
				if !isDoc {
					errMsg := "Expected YAML document to be annotated with %s but was %T"
					return fmt.Errorf(errMsg, AnnotationDataValues, node)
				}
			}
		}
	}

	for _, childVal := range node.GetValues() {
		err := v.checkNonDocs(childVal)
		if err != nil {
			return err
		}
	}

	return nil
}

func processDVAnnotationArgs(annContent string) (map[string]string, error) {
	result := make(map[string]string)

	if annContent == "" {
		return result, nil
	}

	kwargs := strings.Split(annContent, ",")
	for _, arg := range kwargs {
		tuple := strings.Split(arg, "=")
		if len(tuple) != 2 {
			// The error message from templating gives more info so defer to there
			return nil, nil
		}

		argName := strings.TrimSpace(tuple[0])
		switch argName {
		case "library":
			if tuple[1] == "" {
				return nil, fmt.Errorf("'%s' annotation keyword argument '%s' cannot be empty", AnnotationDataValues, argName)
			}
			result["library"] = tuple[1]
		case "after_library_module":
			if _, err := strconv.ParseBool(tuple[1]); err != nil {
				return nil, fmt.Errorf("'%s' annotation keyword argument '%s' must be a bool", AnnotationDataValues, argName)
			}
			result["after_library_module"] = tuple[1]
		default:
			return nil, fmt.Errorf("Unknown '%s' annotation keyword argument '%s'", AnnotationDataValues, argName)
		}
	}
	return result, nil
}
