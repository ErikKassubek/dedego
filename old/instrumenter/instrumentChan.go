package instrumenter

/*
Copyright (c) 2023, Erik Kassubek
All rights reserved.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*/

/*
Author: Erik Kassubek <erik-kassubek@t-online.de>
Package: dedego-instrumenter
Project: Dynamic Analysis to detect potential deadlocks in concurrent Go programs
*/

/*
instrumentChan.go
Instrument channels to work with the "github.com/ErikKassubek/deadlockDetectorGo/src/dedego" library
*/

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

var selectIdCounter int = 0
var selectCaseCounter int = 0

/*
Type for the select_ops list
@field id int: id of the select statement
@field size int: number of cases (incl. default) of the select
*/
type select_op struct {
	id   int
	size int
}

/*
Tuple to store an index and string
@field index int: index
@field name string: string
*/
type chanRetNil struct {
	index int
	name  string
}

// collect select cases with (id, no of cases)
var select_ops = make([]select_op, 0)

/*
Function to instrument a given ast.File with channels. Channels and operation
of this channels are replaced by there dedego equivalent.
@param f *ast.File: ast file to instrument
@param astSet *token.FileSet: token file set
@param maxTime int: maximum time to wait for the program
@param maxSelectTime int: maximum time to wait for a select operation
@param imports []string: list of imported libraries
@return error: error or nil
*/
func instrument_chan(f *ast.File, astSet *token.FileSet, maxTime int,
	maxSelectTime int, imports []string) error {
	// ast.Print(astSet, f)
	// add the import of the dedego library
	add_dedego_import(f)

	var main_func *ast.FuncDecl

	// first pass-through to instrument main function and function declarations
	astutil.Apply(f, nil, func(c *astutil.Cursor) bool {
		n := c.Node()

		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Name.Obj != nil && n.Name.Obj.Name == "main" {
				add_dedego_fetch_order(f)
				add_run_analyzer(n)
				add_init_call(n, maxTime)
				main_func = n
			} else {
				instrument_function_declarations(n, c)
			}
		case *ast.FuncType:
			instrument_function_type(n, c)
		}
		return true
	})

	// second pass-through to instrument everything else
	astutil.Apply(f, nil, func(c *astutil.Cursor) bool {
		n := c.Node()

		switch n := n.(type) {
		case *ast.GenDecl: // instrument declarations of structs, interfaces, chan.
			instrument_gen_decl(n, c, astSet)
		case *ast.AssignStmt: // handle assign statements
			switch n.Rhs[0].(type) {
			case *ast.UnaryExpr: // receive with assign
				instrument_receive_with_assign(n, c)
			case *ast.CompositeLit: // creation of struct
				instrument_assign_struct(n)
			case *ast.Ident: // nil
				instrument_nil_assign(n, astSet)
			}
		case *ast.CallExpr: // call expression
			instrument_call_expressions(n, c, astSet, imports)
		case *ast.SendStmt: // handle send messages
			instrument_send_statement(n, c)
		case *ast.ExprStmt: // handle receive and close
			instrument_expression_statement(n, c)
		case *ast.DeferStmt: // handle defer
			instrument_defer_statement(n, c)
		case *ast.GoStmt: // handle the creation of new go routines
			instrument_go_statements(n, c)
		case *ast.SelectStmt: // handel select statements
			instrument_select_statements(n, c, maxSelectTime, astSet)
		case *ast.RangeStmt: // range
			instrument_range_stm(n)
		case *ast.ReturnStmt: // return <- c
			instrument_return(n)
		case *ast.IfStmt: // if a == nil {
			instrument_if(n, astSet)
		}

		return true
	})
	if len(select_ops) > 0 {
		add_order_in_main(main_func)
	}

	return nil
}

/*
Function to add the import of the dedego library
@param n *ast.File: ast file to instrument
*/
func add_dedego_import(n *ast.File) {
	import_spec := &ast.ImportSpec{
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: "\"github.com/ErikKassubek/deadlockDetectorGo/src/dedego\"",
		},
	}

	if n.Decls == nil {
		n.Decls = []ast.Decl{}
	}

	if len(n.Decls) == 0 {
		n.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT}}, n.Decls...)
	}

	switch n.Decls[0].(type) {
	case (*ast.GenDecl):
		if n.Decls[0].(*ast.GenDecl).Tok != token.IMPORT {
			n.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT}}, n.Decls...)
		}
	default:
		n.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT}}, n.Decls...)
	}

	switch n.Decls[0].(type) {
	case *ast.GenDecl:
	default:
		n.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT}}, n.Decls...)
	}

	if n.Decls[0].(*ast.GenDecl).Specs == nil {
		n.Decls[0].(*ast.GenDecl).Specs = []ast.Spec{}
	}

	n.Decls[0].(*ast.GenDecl).Specs = append(n.Decls[0].(*ast.GenDecl).Specs, import_spec)
}

/*
Add var dedegoFetchOrder = make(map[int]int) as global variable
@param n *ast.File: ast file
*/
func add_dedego_fetch_order(n *ast.File) {
	n.Decls = append(n.Decls, &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					{
						Name: "dedegoFetchOrder = make(map[int]int)",
					},
				},
			},
		},
	})
}

/*
Function to add call of dedego.Init(), defer time.Sleep(time.Millisecond)
and defer dedego.RunAnalyzer() to the main function. The time.Sleep call is used
to give the go routines a chance to finish there execution.
@param n *ast.FuncDecl: node of the main function declaration of the ast
*/
func add_init_call(n *ast.FuncDecl, maxTime int) {
	body := n.Body.List
	if body == nil {
		return
	}

	body = append([]ast.Stmt{
		&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.Ident{
					Name: "dedego.Init",
				},
				Args: []ast.Expr{
					&ast.Ident{
						Name: strconv.Itoa(maxTime),
					},
				},
			},
		},
	}, body...)
	n.Body.List = body
}

/*
Function to add order inforcement structure and command line argument receiver
@param n *ast.FuncDecl: main function declaration in ast
@return: nil
*/
func add_order_in_main(n *ast.FuncDecl) {
	if n == nil || n.Body == nil {
		return
	}

	body := n.Body.List

	if body == nil {
		return
	}

	body = append([]ast.Stmt{
		&ast.ExprStmt{
			X: &ast.Ident{
				Name: "var order string",
			},
		},
		&ast.ExprStmt{
			X: &ast.Ident{
				Name: "if len(os.Args) > 0 { order = os.Args[1] }",
			},
		},
		&ast.ExprStmt{
			X: &ast.Ident{
				Name: "order_split := strings.Split(order, \";\")",
			},
		},
		&ast.ExprStmt{
			X: &ast.Ident{
				Name: "for _, ord := range order_split { " +
					"ord_split := strings.Split(ord, \",\"); " +
					"id, err1 := strconv.Atoi(ord_split[0]);" +
					"c, err2 := strconv.Atoi(ord_split[1]);" +
					"if (err1 == nil && err2 == nil) {" +
					"dedegoFetchOrder[id] = c}}",
			},
		},
	}, body...)

	n.Body.List = body
}

/*
Add a defer statement to run the analyzer
@param n *ast.FuncDecl: ast function declaration
*/
func add_run_analyzer(n *ast.FuncDecl) {
	n.Body.List = append([]ast.Stmt{
		&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.Ident{
					Name: "defer dedego.RunAnalyzer",
				},
			},
		},
		&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.Ident{
					Name: "defer time.Sleep",
				},
				Args: []ast.Expr{
					&ast.Ident{
						Name: "time.Millisecond",
					},
				},
			},
		},
	},
		n.Body.List...)

}

/*
Function to instrument the declarations of channels, structs and interfaces.
@param n *ast.GenDecl: node of the declaration in the ast
@param c *astutil.Cursor: cursor that traverses the ast
*/
func instrument_gen_decl(n *ast.GenDecl, c *astutil.Cursor, astSet *token.FileSet) {
	// ast.Print(astSet, n)
	for i, s := range n.Specs {
		switch s_type := s.(type) {
		case *ast.ValueSpec:
			switch t_type := s_type.Type.(type) {
			case *ast.ChanType:
				type_val := get_name(t_type.Value)
				if !strings.Contains(type_val, "time.") {
					n.Specs[i].(*ast.ValueSpec).Type = &ast.Ident{
						Name: "= dedego.NewChan[" + type_val + "](0)",
					}
				}
			}
		case *ast.TypeSpec:
			switch s_type_type := s_type.Type.(type) {
			case *ast.StructType:
				for j, t := range s_type_type.Fields.List {
					switch t_type := t.Type.(type) {
					case *ast.ChanType:
						type_val := get_name(t_type.Value)
						n.Specs[i].(*ast.TypeSpec).Type.(*ast.StructType).Fields.List[j].Type = &ast.Ident{
							Name: "dedego.Chan[" + type_val + "]",
						}
					case *ast.ArrayType:
						switch elt := t_type.Elt.(type) {
						case *ast.ChanType:
							type_string := get_name(elt.Value)
							n.Specs[i].(*ast.TypeSpec).Type.(*ast.StructType).Fields.List[j].Type.(*ast.ArrayType).Elt = &ast.Ident{
								Name: "dedego.Chan[" + type_string + "]",
							}
						}
					}
				}
			case *ast.InterfaceType:
				for _, t := range s_type_type.Methods.List {
					switch t_type := t.Type.(type) {
					case *ast.FuncType:

						_ = instrument_function_declaration_return_values(t_type)
						instrument_function_declaration_parameter(t_type)
					}
				}
			}

		}
	}
}

/*
Function to instrument function types outside of function declarations.
@param n *ast.FuncDecl: node of the func declaration in the ast
@param c *astutil.Cursor: cursor that traverses the ast
*/
func instrument_function_type(n *ast.FuncType, c *astutil.Cursor) {
	_ = instrument_function_declaration_return_values(n)
	instrument_function_declaration_parameter(n)
}

/*
Function to instrument function declarations.
@param n *ast.FuncDecl: node of the func declaration in the ast
@param c *astutil.Cursor: cursor that traverses the ast
*/
func instrument_function_declarations(n *ast.FuncDecl, c *astutil.Cursor) {
	val := instrument_function_declaration_return_values(n.Type)
	instrument_function_declaration_parameter(n.Type)

	// change nil
	if len(val) != 0 {
		astutil.Apply(n, nil, func(c *astutil.Cursor) bool {
			node := c.Node()
			switch ret := node.(type) {
			case *ast.ReturnStmt:
				for _, elem := range val {
					switch r := ret.Results[elem.index].(type) {
					case *ast.Ident:
						if r.Name == "nil" {
							node.(*ast.ReturnStmt).Results[elem.index].(*ast.Ident).Name = elem.name + "{}"
						}
					}
				}
			}
			return true
		})
	}
}

/*
Function to change the return value of functions if they contain a chan.
@param n *ast.FuncType: node of the func type in the ast
*/
func instrument_function_declaration_return_values(n *ast.FuncType) []chanRetNil {
	astResult := n.Results

	// do nothing if the functions does not have return values
	if astResult == nil {
		return []chanRetNil{}
	}
	r := make([]chanRetNil, 0)

	// traverse all return types
	for i, res := range n.Results.List {
		switch res.Type.(type) {
		case *ast.ChanType: // do not call continue if channel
		default:
			continue // continue if not a channel
		}

		translated_string := ""
		name := get_name(res.Type.(*ast.ChanType).Value)
		translated_string = "dedego.Chan[" + name + "]"
		r = append(r, chanRetNil{i, translated_string})

		// set the translated value
		n.Results.List[i] = &ast.Field{
			Type: &ast.Ident{
				Name: translated_string,
			},
		}
	}
	return r
}

/*
Function to instrument the parameter value of functions if they contain a chan.
@param n *ast.FuncType: node of the func type in the ast
*/
func instrument_function_declaration_parameter(n *ast.FuncType) {
	astParam := n.Params

	// do nothing if the functions does not have return values
	if astParam == nil {
		return
	}

	// traverse all parameters
	for i, res := range astParam.List {
		switch res.Type.(type) {
		case *ast.ChanType: // do not call continue if channel
		default:
			continue // continue if not a channel
		}

		translated_string := ""
		switch v := res.Type.(*ast.ChanType).Value.(type) {
		case *ast.Ident: // chan <type>
			translated_string = "dedego.Chan[" + v.Name + "]"
		case *ast.StructType:
			translated_string = "dedego.Chan[struct{}]"
		case *ast.ArrayType:
			translated_string = "dedego.Chan[[]" + get_name(v.Elt) + "]"
		}

		// set the translated value
		n.Params.List[i] = &ast.Field{
			Names: n.Params.List[i].Names,
			Type: &ast.Ident{
				Name: translated_string,
			},
		}
	}
}

/*
Insturment receive with assign
@param n *ast.AssignStmt: assign statment
@param c *astutil.Cursor: ast cursor
*/
func instrument_receive_with_assign(n *ast.AssignStmt, c *astutil.Cursor) {
	if n.Rhs[0].(*ast.UnaryExpr).Op != token.ARROW {
		return
	}

	variable := get_name(n.Lhs[0])
	receiveStmt := ".Receive"

	if len(n.Lhs) > 1 {
		variable += ", " + get_name(n.Lhs[1])
		receiveStmt = ".ReceiveOk"
	}

	channel := get_name(n.Rhs[0].(*ast.UnaryExpr).X)

	token := n.Tok
	c.Replace(&ast.AssignStmt{
		Lhs: []ast.Expr{
			&ast.Ident{
				Name: variable,
			},
		},
		Tok: token,
		Rhs: []ast.Expr{
			&ast.CallExpr{
				Fun: &ast.Ident{
					Name: channel + receiveStmt,
				},
			},
		},
	})
}

// instrument creation of struct
func instrument_assign_struct(n *ast.AssignStmt) {
	for i, t := range n.Rhs[0].(*ast.CompositeLit).Elts {
		switch t.(type) {
		case *(ast.KeyValueExpr):
		default:
			continue
		}
		switch t.(*ast.KeyValueExpr).Value.(type) {
		case *ast.CallExpr:
		default:
			continue
		}

		t_type := t.(*ast.KeyValueExpr).Value.(*ast.CallExpr)
		if get_name(t_type.Fun) != "make" {
			continue
		}

		switch t_type.Args[0].(type) {
		case *ast.ChanType:
		default:
			continue
		}

		name := get_name(t_type.Args[0].(*ast.ChanType).Value)
		size := "0"
		if len(t_type.Args) > 1 {
			size = get_name(t_type.Args[1])
		}

		n.Rhs[0].(*ast.CompositeLit).Elts[i].(*ast.KeyValueExpr).Value.(*ast.CallExpr).Fun = &ast.Ident{Name: "dedego.NewChan[" + name + "]"}
		n.Rhs[0].(*ast.CompositeLit).Elts[i].(*ast.KeyValueExpr).Value.(*ast.CallExpr).Args = []ast.Expr{&ast.Ident{Name: size}}
	}
}

// instrument range over channel
func instrument_range_stm(n *ast.RangeStmt) {

	// switch n.Rhs[0].Body.List

	get_info_string := "_.GetInfo()"

	varName := get_name(n.Key)
	if strings.Contains(varName, ",") {
		get_info_string = "_.GetInfoOk()"
	}

	if n.Key.(*ast.Ident).Obj == nil {
		return
	}

	switch n.Key.(*ast.Ident).Obj.Decl.(type) {
	case *ast.AssignStmt:
	default:
		return
	}

	l := n.Key.(*ast.Ident).Obj.Decl.(*ast.AssignStmt).Lhs
	if len(l) != 1 {
		return
	}

	r := n.Key.(*ast.Ident).Obj.Decl.(*ast.AssignStmt).Rhs[0]
	switch r.(type) {
	case *ast.UnaryExpr:
	default:
		return
	}

	chanName := get_name(r.(*ast.UnaryExpr).X)

	switch x := n.X.(type) {
	case *ast.Ident:
		switch d := x.Obj.Decl.(type) {
		case *ast.AssignStmt:
			switch r := d.Rhs[0].(type) {
			case *ast.CallExpr:
				switch f := r.Fun.(type) {
				case *ast.IndexExpr:
					switch x1 := f.X.(type) {
					case *ast.SelectorExpr:
						switch x2 := x1.X.(type) {
						case *ast.Ident:
							if x2.Name == "dedego" {
								n.Key.(*ast.Ident).Name = varName + "_"
								n.X = &ast.Ident{Name: chanName + ".GetChan()"}
								n.Body.List = append([]ast.Stmt{
									&ast.ExprStmt{
										X: &ast.Ident{Name: chanName + ".Post(false, " + varName + "_)"},
									},
									&ast.ExprStmt{
										X: &ast.Ident{Name: varName + " := " + varName + get_info_string},
									},
								},
									n.Body.List...)
							}
						}
					}
				case *ast.Ident:
					if strings.HasPrefix(f.Name, "dedego.NewChan") {
						n.Key.(*ast.Ident).Name = varName + "_"
						n.X = &ast.Ident{Name: chanName + ".GetChan()"}
						n.Body.List = append([]ast.Stmt{
							&ast.ExprStmt{
								X: &ast.Ident{Name: chanName + ".Post(false, " + varName + "_)"},
							},
							&ast.ExprStmt{
								X: &ast.Ident{Name: varName + " := " + varName + get_info_string},
							},
						},
							n.Body.List...)
					}
				}

			}
		}
	}
}

func instrument_nil_assign(n *ast.AssignStmt, astSet *token.FileSet) {
	switch n.Rhs[0].(type) {
	case *ast.Ident:
	default:
		return
	}
	if get_name(n.Rhs[0]) != "nil" {
		return
	}
	buf := new(bytes.Buffer)
	name := ""
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "dedego.Chan") {
			name_split := strings.Split(line, "\"")
			if len(name_split) > 1 {
				name = name_split[1]
				break
			}
		}
	}
	if name != "" {
		n.Rhs[0].(*ast.Ident).Name = name + "{}"
	}

}

/*
instrument the call expression. This contains instrumentation of make and
library function calls
@params callExp: the call expression to instrument
@params c: the cursor
@params astSet: the ast file set
@params imports: list of imported libraries
*/
func instrument_call_expressions(callExp *ast.CallExpr, c *astutil.Cursor,
	astSet *token.FileSet, imports []string) {

	switch callExp.Fun.(type) {
	case *ast.Ident:
		instrument_make(callExp)
	case *ast.SelectorExpr:
		instrument_library_function_call(callExp, c, astSet, imports)
	}

}

/*
instrument the make of a channel
@params callExp: the call expression to instrument
*/
func instrument_make(callExp *ast.CallExpr) {
	// don't change call expression of non-make function
	if get_name(callExp.Fun) == "make" {
		switch callExp.Args[0].(type) {
		// make creates a channel
		case *ast.ChanType:
			callExpVal := callExp.Args[0].(*ast.ChanType).Value
			chanType := get_name(callExpVal)

			// set size of channel
			chanSize := "0"
			if len(callExp.Args) >= 2 {
				switch args_type := callExp.Args[1].(type) {
				case *ast.BasicLit:
					chanSize = args_type.Value
				case *ast.Ident:
					chanSize = args_type.Name

				}
			}

			// set function name to dedego.NewChan[<chanType>]
			callExp.Fun.(*ast.Ident).Name = "dedego.NewChan[" + chanType + "]"

			// remove second argument if size was given in make
			if len(callExp.Args) >= 1 {
				callExp.Args = callExp.Args[:1]
			}

			// set function argument to channel size
			callExp.Args[0] = &ast.BasicLit{Kind: token.INT, Value: "int(" + chanSize + ")"}
		}
	}
}

/*
instrument a function call of a library function
@params n: the call expression to instrument
@params c: the cursor
@params astSet: the ast file set
@params imports: list of imported libraries
*/
func instrument_library_function_call(n *ast.CallExpr, c *astutil.Cursor,
	astSet *token.FileSet, imports []string) {
	// check if function call has selector
	switch n.Fun.(type) {
	case *ast.SelectorExpr:
	default:
		return
	}

	sel := n.Fun.(*ast.SelectorExpr)
	// check if selector is in imports
	name := get_name(sel.X)
	// check if selector is in imports
	for _, imp := range imports {
		if name != imp {
			continue
		}
		if n.Args == nil {
			return
		}

		// check find args with type chan
		var channels []string
		var types []string
		var sizes []string

		for i, arg := range n.Args {
			switch arg_type := arg.(type) {
			case *ast.Ident:
				if arg_type.Obj == nil || arg_type.Obj.Decl == nil {
					continue
				}
				decl := arg_type.Obj.Decl
				switch decl_type := decl.(type) {
				case *ast.AssignStmt:
					decl_name := get_name(decl_type.Rhs[0])
					if len(decl_name) >= 14 && decl_name[:14] == "dedego.NewChan" {
						chanName := get_name(decl_type.Lhs[0])
						chanType := strings.Split(decl_name[15:], "]")[0]
						decl_val := decl_type.Rhs[0].(*ast.CallExpr).Args[0].(*ast.BasicLit).Value
						chanSize := decl_val[4 : len(decl_val)-1]
						channels = append(channels, chanName)
						types = append(types, chanType)
						sizes = append(sizes, chanSize)
						n.Args[i] = &ast.Ident{Name: chanName + "_dedego"}
					}
				case *ast.UnaryExpr:
					panic("TODO: Implement unary expression") // TODO: Implement unary expression
				default:
					continue
				}
			}
		}

		if len(channels) == 0 {
			continue
		}

		// get string representation of function
		buf := new(bytes.Buffer)
		printer.Fprint(buf, astSet, n)

		replacement_string := "{\n"

		for i, channel := range channels {
			replacement_string += channel + "_dedego := make(chan " +
				types[i] + ", " + sizes[i] + ")\n"
			replacement_string += "go func() {\n"
			replacement_string += "for {\n"
			replacement_string += "select {\n"
			replacement_string += "case " + "dedego_case := <- " + channel +
				"_dedego:\n" + channel + ".Send(dedego_case)\n"
			replacement_string += "case dedego_case := <-" + channel +
				".GetChan():\n" + channel + ".Post(true, dedego_case)\n" +
				channel + "_dedego <- dedego_case.GetInfo()\n"
			replacement_string += "}\n"
			replacement_string += "if " + channel + ".IsClosed(){\nbreak\n}}}()\n"
		}

		replacement_string += buf.String() + "\n"

		replacement_string += "}"

		c.Replace(&ast.Ident{
			Name: replacement_string,
		})

	}
}

// instrument a send statement
func instrument_send_statement(n *ast.SendStmt, c *astutil.Cursor) {

	// get the channel name
	channel := get_name(n.Chan)

	value := ""

	// get what is send through the channel
	v := n.Value
	// fmt.Printf("%T\n", v)
	call_expr := false
	func_lit := false
	switch lit := v.(type) {
	case (*ast.BasicLit):
		value = lit.Value
	case (*ast.Ident):
		value = lit.Name
	case (*ast.CallExpr):
		call_expr = true
	case *ast.ParenExpr:
		value = n.Chan.(*ast.Ident).Obj.Decl.(*ast.Field).Type.(*ast.ChanType).Value.(*ast.Ident).Name
	case *ast.CompositeLit:
		switch lit_type := lit.Type.(type) {
		case *ast.StructType:
			value = "struct{}{}"
		case *ast.ArrayType:
			value = "[]" + get_name(lit_type.Elt) + "{" + get_name(lit.Elts[0]) + "}"
		case *ast.Ident:
			value = get_name(lit_type) + "{}"
		}
	case *ast.SelectorExpr:
		value = get_selector_expression_name(lit)
	case *ast.UnaryExpr:
		arg_string := ""
		switch x := lit.X.(type) {
		case *ast.CompositeLit:
			for _, a := range x.Elts {
				arg_string += get_name(a) + ","
			}
			switch t_type := x.Type.(type) {
			case *ast.Ident:
				value = lit.Op.String() + t_type.Name + "{" + arg_string + "}"
			case *ast.SelectorExpr:
				value = lit.Op.String() + get_selector_expression_name(t_type) + "{" + arg_string + "}"

			}
		}
	case *ast.FuncLit:
		func_lit = true
	case *ast.IndexExpr:
		value = get_name(lit.X) + "[" + get_name(lit.Index)
	default:
		errString := fmt.Sprintf("Unknown type %T in instrument_send_statement2", v)
		panic(errString)
	}

	// replace with function call
	if call_expr {
		c.Replace(&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: channel,
					},
					Sel: &ast.Ident{
						Name: "Send",
					},
				},
				Lparen: token.NoPos,
				Args: []ast.Expr{
					v.(*ast.CallExpr),
				},
			},
		})
	} else if func_lit {
		c.Replace(&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: channel,
					},
					Sel: &ast.Ident{
						Name: "Send",
					},
				},
				Lparen: token.NoPos,
				Args: []ast.Expr{
					v.(*ast.FuncLit),
				},
			},
		})
	} else {
		c.Replace(&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: channel,
					},
					Sel: &ast.Ident{
						Name: "Send",
					},
				},
				Lparen: token.NoPos,
				Args: []ast.Expr{
					&ast.Ident{
						Name: value,
					},
				},
			},
		})
	}
}

// instrument receive and call statements
func instrument_expression_statement(n *ast.ExprStmt, c *astutil.Cursor) {
	x_part := n.X
	switch x_part.(type) {
	case *ast.UnaryExpr:
		instrument_receive_statement(n, c)
	case *ast.CallExpr:
		instrument_close_statement(n, c)
	case *ast.TypeAssertExpr, *ast.Ident:
	default:
		errString := fmt.Sprintf("Unknown type %T in instrument_expression_statement", x_part)
		panic(errString)
	}
}

// instrument defer state,emt
func instrument_defer_statement(n *ast.DeferStmt, c *astutil.Cursor) {
	x_call := n.Call.Fun
	switch fun_type := x_call.(type) {
	case *ast.Ident:
		if fun_type.Name == "close" && len(n.Call.Args) > 0 {
			name := get_name(n.Call.Args[0])
			c.Replace(&ast.DeferStmt{
				Call: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: name + ".Close",
					},
				},
			})
		}
	}
}

// instrument receive statements
func instrument_receive_statement(n *ast.ExprStmt, c *astutil.Cursor) {
	x_part := n.X.(*ast.UnaryExpr)

	// check if correct operation
	if x_part.Op != token.ARROW {
		return
	}
	if is_time_element(x_part) {
		return
	}

	// get channel name
	var channel string
	switch x_part_x := x_part.X.(type) {
	case *ast.Ident:
		channel = x_part_x.Name
	case *ast.CallExpr:
		channel = get_name(x_part_x)
	case *ast.SelectorExpr:
		channel = get_selector_expression_name(x_part_x)
	default:
		errString := fmt.Sprintf("Unknown type %T in instrument_receive_statement", x_part)
		panic(errString)
	}

	if !(len(channel) > 11 && channel[:11] == "time.After(") {
		c.Replace(&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.Ident{
						Name: channel,
					},
					Sel: &ast.Ident{
						Name: "Receive",
					},
				},
			},
		})
	}
}

func is_time_element(n *ast.UnaryExpr) bool {
	switch x_part_x := n.X.(type) {
	case *ast.Ident:
		if x_part_x.Obj == nil || x_part_x.Obj.Decl == nil {
			return false
		}
		switch decl := x_part_x.Obj.Decl.(type) {
		case *ast.ValueSpec:
			switch v := decl.Type.(type) {
			case *ast.ChanType:
				switch x := v.Value.(type) {
				case *ast.SelectorExpr:
					if get_name(x.X) == "time" {
						return true
					}
				}
			}
		}
	case *ast.SelectorExpr:
		switch x := x_part_x.X.(type) {
		case *ast.Ident:
			switch decl := x.Obj.Decl.(type) {
			case *ast.AssignStmt:
				switch r := decl.Rhs[0].(type) {
				case *ast.CallExpr:
					switch f := r.Fun.(type) {
					case *ast.SelectorExpr:
						switch i := f.X.(type) {
						case *ast.Ident:
							if i.Name == "time" {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

// change close statements to dedego.Close
func instrument_close_statement(n *ast.ExprStmt, c *astutil.Cursor) {
	x_part := n.X.(*ast.CallExpr)

	// return if not ident
	wrong := true
	switch x_part.Fun.(type) {
	case *ast.Ident:
		wrong = false
	}
	if wrong {
		return
	}

	// remove all non close statements
	if x_part.Fun.(*ast.Ident).Name != "close" || len(x_part.Args) == 0 {
		return
	}

	channel := get_name(x_part.Args[0])
	c.Replace(&ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.Ident{
					Name: channel,
				},
				Sel: &ast.Ident{
					Name: "Close",
				},
			},
		},
	})
}

// instrument the creation of new go routines
func instrument_go_statements(n *ast.GoStmt, c *astutil.Cursor) {
	var_name := "DedegoRoutineIndex"

	var func_body *ast.BlockStmt
	switch t := n.Call.Fun.(type) {
	case *ast.FuncLit:
		func_body = &ast.BlockStmt{
			List: t.Body.List,
		}
	case *ast.Ident:
		if t.Name == "close" && len(n.Call.Args) == 1 {
			func_body = &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ExprStmt{
						X: &ast.CallExpr{
							Fun:  &ast.Ident{Name: get_name(n.Call.Args[0]) + ".Close"},
							Args: []ast.Expr{&ast.Ident{Name: ""}},
						},
					},
				},
			}
		} else {
			func_body = &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ExprStmt{
						X: &ast.CallExpr{
							Fun:  n.Call.Fun,
							Args: n.Call.Args,
						},
					},
				},
			}
		}
	case *ast.SelectorExpr, *ast.CallExpr:
		func_body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun:  n.Call.Fun,
						Args: n.Call.Args,
					},
				},
			},
		}

	default:
		fmt.Printf("Unknown Type %T in instrument_go_statement", n.Call.Fun)
	}

	n = &ast.GoStmt{
		Call: &ast.CallExpr{
			Fun:  n.Call.Fun,
			Args: n.Call.Args,
		},
	}

	fc := n.Call.Fun
	switch fc_type := fc.(type) {
	case *ast.FuncLit: // go with lambda
		_ = instrument_function_declaration_return_values(fc_type.Type)
		instrument_function_declaration_parameter(fc_type.Type)
	default:
		n.Call.Args = nil
	}

	params := &ast.FieldList{}
	switch n.Call.Fun.(type) {
	case *ast.FuncLit:
		params = n.Call.Fun.(*ast.FuncLit).Type.Params
	}

	// add PreSpawn
	c.Replace(&ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.FuncLit{
				Type: &ast.FuncType{},
				Body: &ast.BlockStmt{
					List: []ast.Stmt{
						&ast.AssignStmt{
							Lhs: []ast.Expr{
								&ast.Ident{
									Name: var_name,
								},
							},
							Tok: token.DEFINE,
							Rhs: []ast.Expr{
								&ast.CallExpr{
									Fun: &ast.Ident{
										Name: "dedego.SpawnPre",
									},
								},
							},
						},
						&ast.GoStmt{
							Call: &ast.CallExpr{
								Fun: &ast.FuncLit{
									Type: &ast.FuncType{
										Params: params,
									},
									Body: &ast.BlockStmt{
										List: []ast.Stmt{
											&ast.ExprStmt{
												X: &ast.CallExpr{
													Fun: &ast.Ident{
														Name: "dedego.SpawnPost",
													},
													Args: []ast.Expr{
														&ast.Ident{
															Name: var_name,
														},
													},
												},
											},
											func_body,
										},
									},
								},
								Args: n.Call.Args,
							},
						},
					},
				},
			},
		},
	})

}

// instrument select statements
func instrument_select_statements(n *ast.SelectStmt, cur *astutil.Cursor,
	selectTime int, astSet *token.FileSet) {
	// collect cases and replace <-i with i.GetChan()
	selectIdCounter++
	select_id := selectIdCounter
	caseNodes := n.Body.List
	cases := make([]string, 0)
	cases_receive := make([]string, 0)
	d := false // check weather select contains default
	sendVar := make([]struct {
		assign_name string
		message     string
	}, 0)
	for i, c := range caseNodes {
		// only look at communication cases
		switch c.(type) {
		case *ast.CommClause:
		default:
			continue
		}

		// check for default, add dedego.PostDefault if found
		if c.(*ast.CommClause).Comm == nil {
			d = true
			c.(*ast.CommClause).Body = append([]ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: &ast.Ident{
							Name: "dedego.PostDefault",
						},
					},
				}}, c.(*ast.CommClause).Body...)
			continue
		}

		var name string
		var assign_name string
		var rec string

		switch c_type := c.(*ast.CommClause).Comm.(type) {
		case *ast.ExprStmt: // receive in switch without assign
			switch c_type.X.(type) {
			case *ast.CallExpr:

				f := c_type.X.(*ast.CallExpr).Fun.(*ast.SelectorExpr)

				if f.Sel.Name == "Receive" {
					name = get_name(f.X)
					cases = append(cases, name)
					assign_name = get_select_case_name()
					cases_receive = append(cases_receive, "true")
					rec = "true"

					if !(len(name) > 11 && name[:11] == "time.After(") {
						n.Body.List[i].(*ast.CommClause).Comm.(*ast.ExprStmt).X = &ast.CallExpr{
							Fun: &ast.Ident{
								Name: assign_name + ":=<-" + name + ".GetChan",
							},
						}
					}
				} else if f.Sel.Name == "Send" {
					name = get_name(f.X)
					cases = append(cases, name)

					assign_name = get_select_case_name()
					cases_receive = append(cases_receive, "false")
					rec = "false"

					arg_val := get_name(c_type.X.(*ast.CallExpr).Args[0])
					sendVar = append(sendVar, struct {
						assign_name string
						message     string
					}{assign_name, arg_val})

					n.Body.List[i].(*ast.CommClause).Comm.(*ast.ExprStmt).X = &ast.Ident{
						Name: name + ".GetChan() <-" + assign_name,
					}
				} else {
					continue
				}
			}

		case *ast.AssignStmt: // receive with assign
			assign_name = get_select_case_name()
			assigned_name := get_name(c_type.Lhs[0])

			get_info_string := "GetInfo()"

			if strings.Contains(assigned_name, ",") {
				get_info_string = "GetInfoOk()"
			}

			cases_receive = append(cases_receive, "true")
			rec = "true"

			f := c_type.Rhs[0]
			names := strings.Split(f.(*ast.CallExpr).Fun.(*ast.Ident).Name, ".")
			for i, n := range names {
				if i != len(names)-1 {
					name += n + "."
				}
			}

			name = strings.TrimSuffix(name, ".")
			cases = append(cases, name)

			c.(*ast.CommClause).Comm.(*ast.AssignStmt).Lhs[0] = &ast.Ident{
				Name: assign_name,
			}

			c.(*ast.CommClause).Comm.(*ast.AssignStmt).Rhs[0] = &ast.Ident{
				Name: "<-" + name + ".GetChan()",
			}
			c.(*ast.CommClause).Body = append([]ast.Stmt{
				&ast.AssignStmt{
					Lhs: []ast.Expr{
						&ast.Ident{
							Name: assigned_name,
						},
					},
					Tok: c_type.Tok,
					Rhs: []ast.Expr{
						&ast.SelectorExpr{
							X: &ast.Ident{
								Name: assign_name,
							},
							Sel: &ast.Ident{
								Name: get_info_string,
							},
						},
					},
				},
			}, c.(*ast.CommClause).Body...)
			c.(*ast.CommClause).Comm.(*ast.AssignStmt).Tok = token.DEFINE
		}

		// add post select
		if name != "" {

			c.(*ast.CommClause).Body = append([]ast.Stmt{
				&ast.ExprStmt{
					X: &ast.Ident{
						Name: name + ".Post(" + rec + ", " + assign_name + ")",
					},
				},
			}, c.(*ast.CommClause).Body...)
		}
	}

	// add cases to preSelect
	cases_string := "false, "
	if d {
		cases_string = "true, "
	}
	for i, c := range cases {
		if len(c) > 11 && c[:11] == "time.After(" {
			continue
		}
		cases_string += (c + ".GetIdPre(" + cases_receive[i] + ")")
		if i != len(cases)-1 {
			cases_string += ", "
		}
	}

	// add sender variable definitions
	// cur.Replace() and preselect
	block := &ast.BlockStmt{
		List: []ast.Stmt{
			&ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.Ident{
						Name: "dedego.PreSelect",
					},
					Args: []ast.Expr{
						&ast.Ident{
							Name: cases_string,
						},
					},
				},
			},
		},
	}

	for _, c := range sendVar {
		block.List = append(block.List, &ast.AssignStmt{
			Lhs: []ast.Expr{
				&ast.Ident{Name: c.assign_name},
			},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.Ident{Name: "dedego.BuildMessage(" + c.message + ")"},
			},
		})
	}

	block.List = append(block.List, n)

	// transform to select with switch
	var original_select *ast.SelectStmt
	var original_select_index int
	b := false
	for i, c := range block.List {
		switch c_type := c.(type) {
		case *ast.SelectStmt:
			original_select = c_type
			original_select_index = i
			b = true
		default:
		}
		if b {
			break
		}
	}

	switch_statement := &ast.SwitchStmt{
		Tag:  &ast.Ident{Name: "dedegoFetchOrder[" + fmt.Sprint(select_id) + "]"},
		Body: &ast.BlockStmt{},
	}

	for i, c := range block.List[original_select_index].(*ast.SelectStmt).Body.List {
		switch_statement.Body.List = append(switch_statement.Body.List,
			&ast.CaseClause{
				List: []ast.Expr{&ast.Ident{Name: fmt.Sprint(i)}},
				Body: []ast.Stmt{&ast.SelectStmt{
					Body: &ast.BlockStmt{
						List: []ast.Stmt{
							c,
							&ast.CommClause{
								Comm: &ast.ExprStmt{
									X: &ast.CallExpr{
										Fun: &ast.Ident{
											Name: "<- time.After",
										},
										Args: []ast.Expr{
											&ast.Ident{
												Name: strconv.Itoa(selectTime) +
													" * time.Second"}},
									},
								},
								Body: []ast.Stmt{original_select},
							},
						},
					},
				}},
			})
	}
	switch_statement.Body.List = append(switch_statement.Body.List,
		&ast.CaseClause{
			Body: []ast.Stmt{
				original_select,
			},
		})

	j := false
	for i, c := range block.List {
		switch c.(type) {
		case *ast.SelectStmt:
			block.List[i] = switch_statement
			j = true
		}
		if j {
			break
		}
	}

	cur.Replace(block)

	// collect select options
	size := len(cases)
	if d {
		size++
	}
	// ast.Print(astSet, block)
	select_ops = append(select_ops, select_op{id: select_id, size: size})
}

/*
Instrument return statemens consisting of an channel receive
@param n *ast.ReturnStmt: return statement
*/
func instrument_return(n *ast.ReturnStmt) {
	if n.Results == nil {
		return
	}

	for i, res := range n.Results {
		switch res_t := res.(type) {
		case *ast.UnaryExpr:
			if res_t.Op != token.ARROW {
				break
			}
			channelName := get_name(res_t.X)
			n.Results[i] = &ast.Ident{
				Name: channelName + ".Receive()",
			}
		}
	}
}

/*
Instrument if statement containing nil
@param n *ast.IfStmt: ast if statement
*/
func instrument_if(n *ast.IfStmt, astSet *token.FileSet) {
	switch n.Cond.(type) {
	case *ast.BinaryExpr:
	default:
		return
	}

	switch n.Cond.(*ast.BinaryExpr).Y.(type) {
	case *ast.Ident:
	default:
		return
	}

	if n.Cond.(*ast.BinaryExpr).Y.(*ast.Ident).Name == "nil" {
		// ast.Print(astSet, n.Cond.(*ast.BinaryExpr).X)
		switch x_type := n.Cond.(*ast.BinaryExpr).X.(type) {
		case *ast.Ident:
			switch decl_type := x_type.Obj.Decl.(type) {
			case *ast.ValueSpec:
				if get_name(decl_type.Type) == "error" {
					return
				}
			case *ast.Field:
				if get_name(decl_type.Type) == "error" {
					return
				}
			}
		}
		name := get_name(n.Cond.(*ast.BinaryExpr).X)
		if name == "err" {
			return
		}
		n.Cond.(*ast.BinaryExpr).X = &ast.Ident{Name: name + ".GetChan()"}
		return
	}

	buf := new(bytes.Buffer)
	ast.Fprint(buf, astSet, n.Cond.(*ast.BinaryExpr).X, nil)
	name := ""
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "dedego.Chan") {
			name_split := strings.Split(line, "\"")
			if len(name_split) > 1 {
				name = name_split[1]
				break
			}
		}
	}
	if name != "" {
		n.Cond.(*ast.BinaryExpr).Y.(*ast.Ident).Name = "(" + name + "{})"
	}

}

/*
Get the name of an element
@param n ast.Expr: element to get the name from
@return string: name
*/
func get_name(n ast.Expr) string {
	if n == nil {
		return ""
	}
	switch n_type := n.(type) {
	case *ast.Ident:
		return n_type.Name
	case *ast.SelectorExpr:
		return get_selector_expression_name(n_type)
	case *ast.StarExpr:
		return "*" + get_name(n.(*ast.StarExpr).X)
	case *ast.CallExpr:
		arguments := make([]string, 0)
		for _, a := range n.(*ast.CallExpr).Args {
			arguments = append(arguments, get_name(a))
		}
		name := get_name(n.(*ast.CallExpr).Fun) + "("
		for _, a := range arguments {
			name += a + ", "
		}
		name += ")"
		return name
	case *ast.ParenExpr:
		return get_name(n_type.X)
	case *ast.TypeAssertExpr:
		return get_name(n_type.Type)
	case *ast.FuncType:
		name := "func("
		if n_type.Params != nil {
			for _, a := range n_type.Params.List {
				name += get_name(a.Type) + ", "
			}
		}
		name += ")"
		if n_type.Results != nil {
			name += "("
			for _, a := range n_type.Results.List {
				name += get_name(a.Type) + ", "
			}
			name += ")"
		}
		return name
	case *ast.FuncLit:
		return get_name(n_type.Type)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.ArrayType:
		return "[]" + get_name(n_type.Elt)
	case *ast.BasicLit:
		return n_type.Value
	case *ast.ChanType:
		return "dedego.Chan[" + get_name(n_type.Value) + "]"
	case *ast.StructType:
		var struct_elem string
		for i, elem := range n_type.Fields.List {
			if len(elem.Names) > 0 {
				struct_elem += get_name(elem.Names[0]) + " " + get_name(elem.Type)
				if i == len(n_type.Fields.List)-1 {
					struct_elem += ", "
				}
			}
		}
		return "struct{" + struct_elem + "}"
	case *ast.IndexExpr:
		return get_name(n_type.X) + "[" + get_name(n_type.Index) + "]"
	case *ast.BinaryExpr:
		return get_name(n_type.X) + n_type.Op.String() + get_name(n_type.Y)
	case *ast.UnaryExpr:
		return n_type.Op.String() + get_name(n_type.X)
	case *ast.MapType:
		return "map[" + get_name(n_type.Key) + "]" + get_name(n_type.Value)
	case *ast.Ellipsis:
		return "..." + get_name(n_type.Elt)
	case *ast.CompositeLit:
		return get_name(n_type.Type)
	default:
		return ""
	}
}

// get the full name of an selector expression
func get_selector_expression_name(n *ast.SelectorExpr) string {
	return get_name(n.X) + "." + n.Sel.Name
}

// get select case name
func get_select_case_name() string {
	selectCaseCounter++
	return "selectCaseDedego_" + strconv.Itoa(selectCaseCounter)
}
