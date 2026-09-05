package skill_test

import (
	"fmt"

	"hop.top/kit/go/core/upgrade/skill"
)

func ExampleInlineFlow() {
	fmt.Println(skill.InlineFlow("mytool", "", skill.SnoozeOnce))
	// Output: [upgrade] run `mytool upgrade --auto`; on decline, snooze; continue task.
}
