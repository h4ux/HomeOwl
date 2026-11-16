package server

// Apple model identifiers and their marketing names, for the NextDNS setup (devmodel.go). NextDNS shows the
// text after the device's name as it is, so it gets "Apple MacBook Air 13-inch (M4)" rather than "Apple Mac16,12".
// Identifiers not listed here go out as they are.

type appleModel struct{ Group, ID, Name string }

var appleModels = []appleModel{
	{"iPhone", "iPhone18,1", "iPhone 17 Pro"},
	{"iPhone", "iPhone18,2", "iPhone 17 Pro Max"},
	{"iPhone", "iPhone18,3", "iPhone 17"},
	{"iPhone", "iPhone18,4", "iPhone Air"},
	{"iPhone", "iPhone17,1", "iPhone 16 Pro"},
	{"iPhone", "iPhone17,2", "iPhone 16 Pro Max"},
	{"iPhone", "iPhone17,3", "iPhone 16"},
	{"iPhone", "iPhone17,4", "iPhone 16 Plus"},
	{"iPhone", "iPhone17,5", "iPhone 16e"},
	{"iPhone", "iPhone16,1", "iPhone 15 Pro"},
	{"iPhone", "iPhone16,2", "iPhone 15 Pro Max"},
	{"iPhone", "iPhone15,4", "iPhone 15"},
	{"iPhone", "iPhone15,5", "iPhone 15 Plus"},
	{"iPhone", "iPhone15,2", "iPhone 14 Pro"},
	{"iPhone", "iPhone15,3", "iPhone 14 Pro Max"},
	{"iPhone", "iPhone14,7", "iPhone 14"},
	{"iPhone", "iPhone14,8", "iPhone 14 Plus"},
	{"iPhone", "iPhone14,2", "iPhone 13 Pro"},
	{"iPhone", "iPhone14,3", "iPhone 13 Pro Max"},
	{"iPhone", "iPhone14,5", "iPhone 13"},
	{"iPhone", "iPhone14,4", "iPhone 13 mini"},
	{"iPhone", "iPhone14,6", "iPhone SE (3rd gen)"},
	{"iPhone", "iPhone13,3", "iPhone 12 Pro"},
	{"iPhone", "iPhone13,4", "iPhone 12 Pro Max"},
	{"iPhone", "iPhone13,2", "iPhone 12"},
	{"iPhone", "iPhone13,1", "iPhone 12 mini"},
	{"iPhone", "iPhone12,3", "iPhone 11 Pro"},
	{"iPhone", "iPhone12,5", "iPhone 11 Pro Max"},
	{"iPhone", "iPhone12,1", "iPhone 11"},
	{"iPad", "iPad16,3", "iPad Pro 11-inch (M4)"},
	{"iPad", "iPad16,5", "iPad Pro 13-inch (M4)"},
	{"iPad", "iPad15,3", "iPad Air 11-inch (M3)"},
	{"iPad", "iPad15,5", "iPad Air 13-inch (M3)"},
	{"iPad", "iPad14,8", "iPad Air 11-inch (M2)"},
	{"iPad", "iPad14,10", "iPad Air 13-inch (M2)"},
	{"iPad", "iPad15,7", "iPad (A16)"},
	{"iPad", "iPad13,18", "iPad (10th gen)"},
	{"iPad", "iPad16,1", "iPad mini (A17 Pro)"},
	{"Mac", "Mac16,12", "MacBook Air 13-inch (M4)"},
	{"Mac", "Mac16,13", "MacBook Air 15-inch (M4)"},
	{"Mac", "Mac15,12", "MacBook Air 13-inch (M3)"},
	{"Mac", "Mac15,13", "MacBook Air 15-inch (M3)"},
	{"Mac", "Mac14,2", "MacBook Air 13-inch (M2)"},
	{"Mac", "Mac14,15", "MacBook Air 15-inch (M2)"},
	{"Mac", "MacBookAir10,1", "MacBook Air (M1)"},
	{"Mac", "Mac16,1", "MacBook Pro 14-inch (M4)"},
	{"Mac", "Mac16,6", "MacBook Pro 14-inch (M4 Pro)"},
	{"Mac", "Mac16,7", "MacBook Pro 16-inch (M4 Pro)"},
	{"Mac", "Mac15,3", "MacBook Pro 14-inch (M3)"},
	{"Mac", "MacBookPro17,1", "MacBook Pro 13-inch (M1)"},
	{"Mac", "Mac16,10", "Mac mini (M4)"},
	{"Mac", "Mac16,11", "Mac mini (M4 Pro)"},
	{"Mac", "Mac14,3", "Mac mini (M2)"},
	{"Mac", "Macmini9,1", "Mac mini (M1)"},
	{"Mac", "Mac16,2", "iMac (M4)"},
	{"Mac", "Mac15,4", "iMac (M3)"},
	{"Mac", "iMac21,1", "iMac (M1)"},
}

// modelName is the marketing name of a model identifier ("" when it isn't listed).
func modelName(id string) string {
	for _, m := range appleModels {
		if m.ID == id {
			return m.Name
		}
	}
	return ""
}

// ndModelLabel is what NextDNS shows as the device's model: "Apple MacBook Air 13-inch (M4)", or "Apple Mac99,1".
func ndModelLabel(id string) string {
	if n := modelName(id); n != "" {
		return "Apple " + n
	}
	return "Apple " + id
}

// modelGroups is the list for the pickers: [{group, models: [{id, name}]}].
func modelGroups() []map[string]any {
	var out []map[string]any
	idx := map[string]int{}
	for _, m := range appleModels {
		i, ok := idx[m.Group]
		if !ok {
			i = len(out)
			idx[m.Group] = i
			out = append(out, map[string]any{"group": m.Group, "models": []map[string]string{}})
		}
		out[i]["models"] = append(out[i]["models"].([]map[string]string), map[string]string{"id": m.ID, "name": m.Name})
	}
	return out
}
