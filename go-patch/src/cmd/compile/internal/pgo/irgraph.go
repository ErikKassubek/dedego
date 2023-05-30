// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A note on line numbers: when working with line numbers, we always use the
// binary-visible relative line number. i.e., the line number as adjusted by
// //line directives (ctxt.InnermostPos(ir.Node.Pos()).RelLine()). Use
// NodeLineOffset to compute line offsets.
//
// If you are thinking, "wait, doesn't that just make things more complex than
// using the real line number?", then you are 100% correct. Unfortunately,
// pprof profiles generated by the runtime always contain line numbers as
// adjusted by //line directives (because that is what we put in pclntab). Thus
// for the best behavior when attempting to match the source with the profile
// it makes sense to use the same line number space.
//
// Some of the effects of this to keep in mind:
//
//  - For files without //line directives there is no impact, as RelLine() ==
//    Line().
//  - For functions entirely covered by the same //line directive (i.e., a
//    directive before the function definition and no directives within the
//    function), there should also be no impact, as line offsets within the
//    function should be the same as the real line offsets.
//  - Functions containing //line directives may be impacted. As fake line
//    numbers need not be monotonic, we may compute negative line offsets. We
//    should accept these and attempt to use them for best-effort matching, as
//    these offsets should still match if the source is unchanged, and may
//    continue to match with changed source depending on the impact of the
//    changes on fake line numbers.
//  - Functions containing //line directives may also contain duplicate lines,
//    making it ambiguous which call the profile is referencing. This is a
//    similar problem to multiple calls on a single real line, as we don't
//    currently track column numbers.
//
// Long term it would be best to extend pprof profiles to include real line
// numbers. Until then, we have to live with these complexities. Luckily,
// //line directives that change line numbers in strange ways should be rare,
// and failing PGO matching on these files is not too big of a loss.

package pgo

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/pgo/internal/graph"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"fmt"
	"internal/profile"
	"os"
)

// IRGraph is the key data structure that is built from profile. It is
// essentially a call graph with nodes pointing to IRs of functions and edges
// carrying weights and callsite information. The graph is bidirectional that
// helps in removing nodes efficiently.
type IRGraph struct {
	// Nodes of the graph
	IRNodes  map[string]*IRNode
	OutEdges IREdgeMap
	InEdges  IREdgeMap
}

// IRNode represents a node in the IRGraph.
type IRNode struct {
	// Pointer to the IR of the Function represented by this node.
	AST *ir.Func
}

// IREdgeMap maps an IRNode to its successors.
type IREdgeMap map[*IRNode][]*IREdge

// IREdge represents a call edge in the IRGraph with source, destination,
// weight, callsite, and line number information.
type IREdge struct {
	// Source and destination of the edge in IRNode.
	Src, Dst       *IRNode
	Weight         int64
	CallSiteOffset int // Line offset from function start line.
}

// NodeMapKey represents a hash key to identify unique call-edges in profile
// and in IR. Used for deduplication of call edges found in profile.
type NodeMapKey struct {
	CallerName     string
	CalleeName     string
	CallSiteOffset int // Line offset from function start line.
}

// Weights capture both node weight and edge weight.
type Weights struct {
	NFlat   int64
	NCum    int64
	EWeight int64
}

// CallSiteInfo captures call-site information and its caller/callee.
type CallSiteInfo struct {
	LineOffset int // Line offset from function start line.
	Caller     *ir.Func
	Callee     *ir.Func
}

// Profile contains the processed PGO profile and weighted call graph used for
// PGO optimizations.
type Profile struct {
	// Aggregated NodeWeights and EdgeWeights across the profile. This
	// helps us determine the percentage threshold for hot/cold
	// partitioning.
	TotalNodeWeight int64
	TotalEdgeWeight int64

	// NodeMap contains all unique call-edges in the profile and their
	// aggregated weight.
	NodeMap map[NodeMapKey]*Weights

	// WeightedCG represents the IRGraph built from profile, which we will
	// update as part of inlining.
	WeightedCG *IRGraph
}

// New generates a profile-graph from the profile.
func New(profileFile string) (*Profile, error) {
	f, err := os.Open(profileFile)
	if err != nil {
		return nil, fmt.Errorf("error opening profile: %w", err)
	}
	defer f.Close()
	profile, err := profile.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("error parsing profile: %w", err)
	}

	if len(profile.Sample) == 0 {
		// We accept empty profiles, but there is nothing to do.
		return nil, nil
	}

	valueIndex := -1
	for i, s := range profile.SampleType {
		// Samples count is the raw data collected, and CPU nanoseconds is just
		// a scaled version of it, so either one we can find is fine.
		if (s.Type == "samples" && s.Unit == "count") ||
			(s.Type == "cpu" && s.Unit == "nanoseconds") {
			valueIndex = i
			break
		}
	}

	if valueIndex == -1 {
		return nil, fmt.Errorf(`profile does not contain a sample index with value/type "samples/count" or cpu/nanoseconds"`)
	}

	g := graph.NewGraph(profile, &graph.Options{
		SampleValue: func(v []int64) int64 { return v[valueIndex] },
	})

	p := &Profile{
		NodeMap: make(map[NodeMapKey]*Weights),
		WeightedCG: &IRGraph{
			IRNodes: make(map[string]*IRNode),
		},
	}

	// Build the node map and totals from the profile graph.
	if err := p.processprofileGraph(g); err != nil {
		return nil, err
	}

	if p.TotalNodeWeight == 0 || p.TotalEdgeWeight == 0 {
		return nil, nil // accept but ignore profile with no samples.
	}

	// Create package-level call graph with weights from profile and IR.
	p.initializeIRGraph()

	return p, nil
}

// processprofileGraph builds various maps from the profile-graph.
//
// It initializes NodeMap and Total{Node,Edge}Weight based on the name and
// callsite to compute node and edge weights which will be used later on to
// create edges for WeightedCG.
//
// Caller should ignore the profile if p.TotalNodeWeight == 0 || p.TotalEdgeWeight == 0.
func (p *Profile) processprofileGraph(g *graph.Graph) error {
	nFlat := make(map[string]int64)
	nCum := make(map[string]int64)
	seenStartLine := false

	// Accummulate weights for the same node.
	for _, n := range g.Nodes {
		canonicalName := n.Info.Name
		nFlat[canonicalName] += n.FlatValue()
		nCum[canonicalName] += n.CumValue()
	}

	// Process graph and build various node and edge maps which will
	// be consumed by AST walk.
	for _, n := range g.Nodes {
		seenStartLine = seenStartLine || n.Info.StartLine != 0

		p.TotalNodeWeight += n.FlatValue()
		canonicalName := n.Info.Name
		// Create the key to the nodeMapKey.
		nodeinfo := NodeMapKey{
			CallerName:     canonicalName,
			CallSiteOffset: n.Info.Lineno - n.Info.StartLine,
		}

		for _, e := range n.Out {
			p.TotalEdgeWeight += e.WeightValue()
			nodeinfo.CalleeName = e.Dest.Info.Name
			if w, ok := p.NodeMap[nodeinfo]; ok {
				w.EWeight += e.WeightValue()
			} else {
				weights := new(Weights)
				weights.NFlat = nFlat[canonicalName]
				weights.NCum = nCum[canonicalName]
				weights.EWeight = e.WeightValue()
				p.NodeMap[nodeinfo] = weights
			}
		}
	}

	if p.TotalNodeWeight == 0 || p.TotalEdgeWeight == 0 {
		return nil // accept but ignore profile with no samples.
	}

	if !seenStartLine {
		// TODO(prattmic): If Function.start_line is missing we could
		// fall back to using absolute line numbers, which is better
		// than nothing.
		return fmt.Errorf("profile missing Function.start_line data (Go version of profiled application too old? Go 1.20+ automatically adds this to profiles)")
	}

	return nil
}

// initializeIRGraph builds the IRGraph by visiting all the ir.Func in decl list
// of a package.
func (p *Profile) initializeIRGraph() {
	// Bottomup walk over the function to create IRGraph.
	ir.VisitFuncsBottomUp(typecheck.Target.Decls, func(list []*ir.Func, recursive bool) {
		for _, n := range list {
			p.VisitIR(n)
		}
	})
}

// VisitIR traverses the body of each ir.Func and use NodeMap to determine if
// we need to add an edge from ir.Func and any node in the ir.Func body.
func (p *Profile) VisitIR(fn *ir.Func) {
	g := p.WeightedCG

	if g.IRNodes == nil {
		g.IRNodes = make(map[string]*IRNode)
	}
	if g.OutEdges == nil {
		g.OutEdges = make(map[*IRNode][]*IREdge)
	}
	if g.InEdges == nil {
		g.InEdges = make(map[*IRNode][]*IREdge)
	}
	name := ir.LinkFuncName(fn)
	node, ok := g.IRNodes[name]
	if !ok {
		node = &IRNode{
			AST: fn,
		}
		g.IRNodes[name] = node
	}

	// Recursively walk over the body of the function to create IRGraph edges.
	p.createIRGraphEdge(fn, node, name)
}

// NodeLineOffset returns the line offset of n in fn.
func NodeLineOffset(n ir.Node, fn *ir.Func) int {
	// See "A note on line numbers" at the top of the file.
	line := int(base.Ctxt.InnermostPos(n.Pos()).RelLine())
	startLine := int(base.Ctxt.InnermostPos(fn.Pos()).RelLine())
	return line - startLine
}

// addIREdge adds an edge between caller and new node that points to `callee`
// based on the profile-graph and NodeMap.
func (p *Profile) addIREdge(callerNode *IRNode, callerName string, call ir.Node, callee *ir.Func) {
	g := p.WeightedCG

	calleeName := ir.LinkFuncName(callee)
	calleeNode, ok := g.IRNodes[calleeName]
	if !ok {
		calleeNode = &IRNode{
			AST: callee,
		}
		g.IRNodes[calleeName] = calleeNode
	}

	nodeinfo := NodeMapKey{
		CallerName:     callerName,
		CalleeName:     calleeName,
		CallSiteOffset: NodeLineOffset(call, callerNode.AST),
	}

	var weight int64
	if weights, ok := p.NodeMap[nodeinfo]; ok {
		weight = weights.EWeight
	}

	// Add edge in the IRGraph from caller to callee.
	edge := &IREdge{
		Src:            callerNode,
		Dst:            calleeNode,
		Weight:         weight,
		CallSiteOffset: nodeinfo.CallSiteOffset,
	}
	g.OutEdges[callerNode] = append(g.OutEdges[callerNode], edge)
	g.InEdges[calleeNode] = append(g.InEdges[calleeNode], edge)
}

// createIRGraphEdge traverses the nodes in the body of ir.Func and add edges between callernode which points to the ir.Func and the nodes in the body.
func (p *Profile) createIRGraphEdge(fn *ir.Func, callernode *IRNode, name string) {
	var doNode func(ir.Node) bool
	doNode = func(n ir.Node) bool {
		switch n.Op() {
		default:
			ir.DoChildren(n, doNode)
		case ir.OCALLFUNC:
			call := n.(*ir.CallExpr)
			// Find the callee function from the call site and add the edge.
			callee := inlCallee(call.X)
			if callee != nil {
				p.addIREdge(callernode, name, n, callee)
			}
		case ir.OCALLMETH:
			call := n.(*ir.CallExpr)
			// Find the callee method from the call site and add the edge.
			callee := ir.MethodExprName(call.X).Func
			p.addIREdge(callernode, name, n, callee)
		}
		return false
	}
	doNode(fn)
}

// WeightInPercentage converts profile weights to a percentage.
func WeightInPercentage(value int64, total int64) float64 {
	return (float64(value) / float64(total)) * 100
}

// PrintWeightedCallGraphDOT prints IRGraph in DOT format.
func (p *Profile) PrintWeightedCallGraphDOT(edgeThreshold float64) {
	fmt.Printf("\ndigraph G {\n")
	fmt.Printf("forcelabels=true;\n")

	// List of functions in this package.
	funcs := make(map[string]struct{})
	ir.VisitFuncsBottomUp(typecheck.Target.Decls, func(list []*ir.Func, recursive bool) {
		for _, f := range list {
			name := ir.LinkFuncName(f)
			funcs[name] = struct{}{}
		}
	})

	// Determine nodes of DOT.
	nodes := make(map[string]*ir.Func)
	for name := range funcs {
		if n, ok := p.WeightedCG.IRNodes[name]; ok {
			for _, e := range p.WeightedCG.OutEdges[n] {
				if _, ok := nodes[ir.LinkFuncName(e.Src.AST)]; !ok {
					nodes[ir.LinkFuncName(e.Src.AST)] = e.Src.AST
				}
				if _, ok := nodes[ir.LinkFuncName(e.Dst.AST)]; !ok {
					nodes[ir.LinkFuncName(e.Dst.AST)] = e.Dst.AST
				}
			}
			if _, ok := nodes[ir.LinkFuncName(n.AST)]; !ok {
				nodes[ir.LinkFuncName(n.AST)] = n.AST
			}
		}
	}

	// Print nodes.
	for name, ast := range nodes {
		if _, ok := p.WeightedCG.IRNodes[name]; ok {
			color := "black"
			if ast.Inl != nil {
				fmt.Printf("\"%v\" [color=%v,label=\"%v,inl_cost=%d\"];\n", ir.LinkFuncName(ast), color, ir.LinkFuncName(ast), ast.Inl.Cost)
			} else {
				fmt.Printf("\"%v\" [color=%v, label=\"%v\"];\n", ir.LinkFuncName(ast), color, ir.LinkFuncName(ast))
			}
		}
	}
	// Print edges.
	ir.VisitFuncsBottomUp(typecheck.Target.Decls, func(list []*ir.Func, recursive bool) {
		for _, f := range list {
			name := ir.LinkFuncName(f)
			if n, ok := p.WeightedCG.IRNodes[name]; ok {
				for _, e := range p.WeightedCG.OutEdges[n] {
					edgepercent := WeightInPercentage(e.Weight, p.TotalEdgeWeight)
					if edgepercent > edgeThreshold {
						fmt.Printf("edge [color=red, style=solid];\n")
					} else {
						fmt.Printf("edge [color=black, style=solid];\n")
					}

					fmt.Printf("\"%v\" -> \"%v\" [label=\"%.2f\"];\n", ir.LinkFuncName(n.AST), ir.LinkFuncName(e.Dst.AST), edgepercent)
				}
			}
		}
	})
	fmt.Printf("}\n")
}

// inlCallee is same as the implementation for inl.go with one change. The change is that we do not invoke CanInline on a closure.
func inlCallee(fn ir.Node) *ir.Func {
	fn = ir.StaticValue(fn)
	switch fn.Op() {
	case ir.OMETHEXPR:
		fn := fn.(*ir.SelectorExpr)
		n := ir.MethodExprName(fn)
		// Check that receiver type matches fn.X.
		// TODO(mdempsky): Handle implicit dereference
		// of pointer receiver argument?
		if n == nil || !types.Identical(n.Type().Recv().Type, fn.X.Type()) {
			return nil
		}
		return n.Func
	case ir.ONAME:
		fn := fn.(*ir.Name)
		if fn.Class == ir.PFUNC {
			return fn.Func
		}
	case ir.OCLOSURE:
		fn := fn.(*ir.ClosureExpr)
		c := fn.Func
		return c
	}
	return nil
}