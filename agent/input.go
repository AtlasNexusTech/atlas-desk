// Shared input handler — calls platform-specific moveMouse/clickMouse/etc.
package main

func handleInput(input map[string]interface{}) {
	action, _ := input["action"].(string)
	switch action {
	case "mouse_move":
		x, _ := input["x"].(float64)
		y, _ := input["y"].(float64)
		moveMouse(int(x), int(y))
	case "mouse_click":
		btn, _ := input["button"].(string)
		clickMouse(btn)
	case "key":
		if k, ok := input["key"].(string); ok { pressKey(k) }
	case "type":
		if t, ok := input["text"].(string); ok { typeText(t) }
	case "scroll":
		dy, _ := input["dy"].(float64)
		scrollMouse(int(dy))
	}
}
