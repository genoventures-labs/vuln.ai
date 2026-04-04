package scanner

import (
	"context"
	"fmt"
	"os"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
)

type StructuralChunk struct {
	Content  string
	Name     string
	Type     string
	StartRow uint32
	EndRow   uint32
}

func GetStructuralChunks(path string, ext string) ([]StructuralChunk, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var lang *sitter.Language
	var queryStr string

	switch strings.ToLower(ext) {
	case ".js", ".jsx", ".ts", ".tsx":
		lang = javascript.GetLanguage()
		queryStr = `
			(function_declaration) @func
			(method_definition) @func
			(arrow_function) @func
			(class_declaration) @class
		`
	case ".py":
		lang = python.GetLanguage()
		queryStr = `
			(function_definition) @func
			(class_definition) @class
		`
	case ".java":
		lang = java.GetLanguage()
		queryStr = `
			(method_declaration) @func
			(class_declaration) @class
		`
	default:
		return nil, fmt.Errorf("unsupported tree-sitter language: %s", ext)
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}

	q, err := sitter.NewQuery([]byte(queryStr), lang)
	if err != nil {
		return nil, err
	}

	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())

	var chunks []StructuralChunk
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		for _, cap := range m.Captures {
			node := cap.Node
			startByte := node.StartByte()
			endByte := node.EndByte()
			
			if int(endByte) > len(content) {
				continue
			}

			chunk := StructuralChunk{
				Content:  string(content[startByte:endByte]),
				StartRow: node.StartPoint().Row,
				EndRow:   node.EndPoint().Row,
				Type:     q.CaptureNameForId(cap.Index),
			}
			
			// Try to find a name if possible (simple heuristic)
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if child.Type() == "identifier" || child.Type() == "name" {
					chunk.Name = string(content[child.StartByte():child.EndByte()])
					break
				}
			}

			chunks = append(chunks, chunk)
		}
	}

	return chunks, nil
}
