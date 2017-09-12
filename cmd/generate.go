// Copyright © 2017 sleepinggenius2 <sleepinggenius2@users.noreply.github.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"bytes"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"
	"github.com/sleepinggenius2/gosmi"
	"github.com/sleepinggenius2/gosmi/types"
	"github.com/spf13/cobra"
)

const fileHeader = `package %s

import (
	"github.com/sleepinggenius2/gosmi"
	"github.com/sleepinggenius2/gosmi/models"
	"github.com/sleepinggenius2/gosmi/types"
)

`
const allowedNodeKinds = types.NodeScalar | types.NodeTable | types.NodeRow | types.NodeColumn | types.NodeNotification

var (
	outDir      string
	outFilename string
	packageName string
	paths       []string
)

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generates Go files from MIBs",
	Long:  `Generates Go files from MIBs.`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		gosmi.Init()
		defer gosmi.Exit()

		for _, path := range paths {
			gosmi.AppendPath(path)
		}

		var out *os.File
		if outFilename == "-" {
			out = os.Stdout
		} else if outFilename != "" {
			var err error
			out, err = os.OpenFile(outFilename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
			if err != nil {
				return errors.Wrapf(err, "Opening file %s", outFilename)
			}
			defer out.Close()
			log.Printf("Outputting to %s\n", outFilename)
		}

		typesMap := make(map[string]*gosmi.Type)

		for i, arg := range args {
			moduleName, err := gosmi.LoadModule(arg)
			if err != nil {
				return errors.Wrapf(err, "Loading module %s", arg)
			}

			module, err := gosmi.GetModule(moduleName)
			if err != nil {
				return errors.Wrapf(err, "Getting module %s", moduleName)
			}

			fileBuf := &bytes.Buffer{}
			if out == nil || i == 0 {
				fmt.Fprintf(fileBuf, fileHeader, packageName)
			}

			generateMibFile(module, fileBuf, typesMap)

			outFile := out
			if outFile == nil {
				filename := path.Join(outDir, strings.ToLower(module.Name)+".go")
				outFile, err = os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
				if err != nil {
					return errors.Wrapf(err, "Opening file %s", filename)
				}
				defer outFile.Close()
				log.Printf("Outputting to %s\n", filename)
			}

			err = writeGoFile(outFile, fileBuf.Bytes())
			if err != nil {
				return errors.Wrap(err, "Writing module Go file")
			}
		}

		typesBuf := &bytes.Buffer{}
		if out == nil {
			fmt.Fprintf(typesBuf, fileHeader, packageName)
		}

		for _, nodeType := range typesMap {
			generateTypeBlock(typesBuf, nodeType, true)
		}

		outFile := out
		if outFile == nil {
			filename := "types.go"
			outFile, err = os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
			if err != nil {
				return errors.Wrapf(err, "Opening file %s", filename)
			}
			defer outFile.Close()
			log.Printf("Outputting to %s\n", filename)
		}

		err = writeGoFile(outFile, typesBuf.Bytes())
		if err != nil {
			return errors.Wrap(err, "Writing types Go file")
		}

		return nil
	},
}

func formatModuleName(moduleName string) (formattedName string) {
	parts := strings.Split(moduleName, "-")
	for _, part := range parts {
		formattedName += strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return
}

func formatNodeName(nodeName string) (formattedName string) {
	return strings.ToUpper(nodeName[:1]) + nodeName[1:]
}

func generateMibFile(module gosmi.Module, buf io.Writer, typesMap map[string]*gosmi.Type) {
	formattedModuleName := formatModuleName(module.Name)
	nodes := module.GetNodes()

	fmt.Fprintf(buf, "type %sModule struct {\n", formattedModuleName)
	for _, node := range nodes {
		if node.Kind&allowedNodeKinds > 0 {
			fmt.Fprintf(buf, "\t%s\tmodels.%sNode\n", formatNodeName(node.Name), node.Kind)
		}
	}
	fmt.Fprintf(buf, "}\n\n")

	fmt.Fprintf(buf, "var %s = %sModule{\n", formattedModuleName, formattedModuleName)
	for _, node := range nodes {
		if node.Kind&allowedNodeKinds > 0 {
			fmt.Fprintf(buf, "\t%s: models.%sNode{\n", formatNodeName(node.Name), node.Kind)
			fmt.Fprintf(buf, "\t\tName: %q,\n", node.Name)
			oid := node.Oid
			oidFormatted := node.RenderNumeric()
			oidLen := node.OidLen
			if node.Kind == types.NodeScalar {
				oid = append(oid, 0)
				oidFormatted += ".0"
				oidLen++
			}
			fmt.Fprintf(buf, "\t\tOid: %#v,\n", oid)
			fmt.Fprintf(buf, "\t\tOidFormatted: %q,\n", oidFormatted)
			fmt.Fprintf(buf, "\t\tOidLen: %d,\n", oidLen)

			if node.Kind&(types.NodeColumn|types.NodeScalar) > 0 {
				switch node.Type.Name {
				case "Integer32", "OctetString", "ObjectIdentifier", "Unsigned32", "Integer64", "Unsigned64", "Enumeration", "Bits":
					generateTypeBlock(buf, node.Type, false)
				default:
					if _, ok := typesMap[node.Type.Name]; !ok {
						typesMap[node.Type.Name] = node.Type
					}
					fmt.Fprintf(buf, "\t\tType: %sType,\n", formatNodeName(node.Type.Name))
				}
			} else if node.Kind == types.NodeTable {
				fmt.Fprintf(buf, "\t\tRow: %s.%s,\n", formattedModuleName, formatNodeName(node.GetRow().Name))
			} else if node.Kind == types.NodeRow {
				fmt.Fprintf(buf, "\t\tColumns: []ColumnNode{\n")
				_, columnOrder := node.GetColumns()
				for _, column := range columnOrder {
					fmt.Fprintf(buf, "\t\t\t%s.%s,\n", formattedModuleName, formatNodeName(column))
				}
				fmt.Fprintf(buf, "\t\t},\n")
				fmt.Fprintf(buf, "\t\tIndex: []ColumnNode{\n")
				indices := node.GetIndex()
				for _, index := range indices {
					indexModule := index.GetModule()
					fmt.Fprintf(buf, "\t\t\t%s.%s,\n", formatModuleName(indexModule.Name), formatNodeName(index.Name))
				}
				fmt.Fprintf(buf, "\t\t},\n")
			} else if node.Kind == types.NodeNotification {
				objects := node.GetNotificationObjects()
				fmt.Fprintf(buf, "\t\tObjects: []ScalarNode{\n")
				for _, object := range objects {
					fmt.Fprintf(buf, "\t\t\t%s.%s,\n", formattedModuleName, formatNodeName(object.Name))
				}
				fmt.Fprintf(buf, "\t\t},\n")
			}

			fmt.Fprintf(buf, "\t},\n")
		}
	}

	fmt.Fprintf(buf, "}\n")
}

func generateTypeBlock(buf io.Writer, t *gosmi.Type, asVar bool) {
	if asVar {
		fmt.Fprintf(buf, "var %sType = gosmi.Type{\n", formatNodeName(t.Name))
	} else {
		fmt.Fprintf(buf, "Type: gosmi.Type{\n")
	}
	fmt.Fprintf(buf, "\tBaseType: types.BaseType%s,\n", t.BaseType)
	if t.Enum != nil {
		fmt.Fprintf(buf, "\tEnum: &gosmi.Enum{\n")
		fmt.Fprintf(buf, "\t\tBaseType: types.BaseType%s,\n", t.Enum.BaseType)
		fmt.Fprintf(buf, "\t\tValues: []gosmi.NamedNumber{\n")
		for _, value := range t.Enum.Values {
			fmt.Fprintf(buf, "\t\t\t%#v,\n", value)
		}
		fmt.Fprintf(buf, "\t\t},\n")
		fmt.Fprintf(buf, "\t},\n")
	}
	if t.Format != "" {
		fmt.Fprintf(buf, "\tFormat: %q,\n", t.Format)
	}
	fmt.Fprintf(buf, "\tName: %q,\n", t.Name)
	if len(t.Ranges) > 0 {
		fmt.Fprintf(buf, "\tRanges: []gosmi.Range{\n")
		for _, typeRange := range t.Ranges {
			fmt.Fprintf(buf, "\t\tgosmi.Range{BaseType: types.BaseType%s, MinValue: %#v, MaxValue: %#v},\n",
				typeRange.BaseType,
				typeRange.MinValue,
				typeRange.MaxValue,
			)
		}
		fmt.Fprintf(buf, "\t},\n")
	}
	if t.Units != "" {
		fmt.Fprintf(buf, "\tUnits: %q,\n", t.Units)
	}
	if asVar {
		fmt.Fprintf(buf, "}\n\n")
	} else {
		fmt.Fprintf(buf, "},\n")
	}
}

func writeGoFile(out io.Writer, b []byte) error {
	formattedSource, err := format.Source(b)
	if err != nil {
		return errors.Wrap(err, "Generating formatted source")
	}

	_, err = out.Write(formattedSource)
	if err != nil {
		return errors.Wrap(err, "Writing file")
	}

	return nil
}

func init() {
	RootCmd.AddCommand(generateCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// generateCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	generateCmd.Flags().StringVarP(&outDir, "dir", "d", ".", "Output directory")
	generateCmd.Flags().StringVarP(&outFilename, "output", "o", "", "Output filename, use - for stdout")
	generateCmd.Flags().StringVarP(&packageName, "package", "p", "mibs", "The package for the generated file")
	generateCmd.Flags().StringSliceVarP(&paths, "path", "M", []string{}, "Path(s) to add to MIB search path")
}
