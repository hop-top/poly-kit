package version_test

import (
	"fmt"

	"hop.top/kit/go/runtime/domain/version"
)

func Example() {
	d := version.NewDAG()
	must(d.Append(version.Version{ID: "v1", Hash: "a1"}))
	must(d.Append(version.Version{ID: "v2", ParentIDs: []string{"v1"}, Hash: "b2"}))
	must(d.Append(version.Version{ID: "v3", ParentIDs: []string{"v1"}, Hash: "c3"})) // diverges from v2

	fmt.Println(d.Heads(), d.IsBranched())
	anc, _ := d.CommonAncestor("v2", "v3")
	fmt.Println(anc)

	// Output:
	// [v2 v3] true
	// v1
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
