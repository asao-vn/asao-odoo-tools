package direct

import (
	"reflect"
	"testing"
)

func TestObjectButtonsReadsOnlyObjectButtons(t *testing.T) {
	arch := `<form string="Booking"><header>
  <button name="action_confirm" type="object" string="Confirm&nbsp;now" invisible="state != 'draft'"/>
  <button name="%(x_salon.action_reschedule)d" type="action" string="Reschedule"/>
  <button name="_private" type="object"/>
  <button special="cancel" string="Discard"/>
</header><sheet><field name="name"/><div><button name="action_done" type="object">Done</button></div></sheet></form>`
	names, err := objectButtonsOf(arch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"action_confirm", "action_done"}) {
		t.Fatalf("buttons: %v", names)
	}
}
