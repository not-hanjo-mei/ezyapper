package web

// activeNavItems returns the standard navigation bar items, with the item
// matching currentPage marked as active by setting its Active field to true.
func activeNavItems(currentPage string) []NavItem {
	return []NavItem{
		{Label: "Dashboard", Href: "/", Icon: "dashboard", Active: currentPage == "dashboard"},
		{Label: "Configuration", Href: "/config", Icon: "settings", Active: currentPage == "config"},
		{Label: "Memories", Href: "/memories", Icon: "memory", Active: currentPage == "memories"},
		{Label: "Profiles", Href: "/profiles", Icon: "person", Active: currentPage == "profiles"},
		{Label: "Channels", Href: "/channels", Icon: "forum", Active: currentPage == "channels"},
		{Label: "Plugins", Href: "/plugins", Icon: "extension", Active: currentPage == "plugins"},
		{Label: "Logs", Href: "/logs", Icon: "description", Active: currentPage == "logs"},
	}
}
