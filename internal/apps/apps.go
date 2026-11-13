// Package apps knows which domains belong to popular apps, so HomeOwl can tell from DNS which apps a device uses
// ("Apps seen on the network") and block one through NextDNS's denylist. Only domains an app doesn't share with
// others are listed: blocking an app must not break anything else (so no google.com, apple.com, amazonaws.com…).
// Domains are registrable ("root") domains; NextDNS blocks their subdomains too.
package apps

import "strings"

type App struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Roots    []string `json:"roots"`
}

var Catalog = []App{
	// social
	{"tiktok", "TikTok", "Social", []string{"tiktok.com", "tiktokv.com", "tiktokcdn.com", "tiktokcdn-us.com", "tiktokv.us", "tiktokw.us", "musical.ly", "byteoversea.com", "ibytedtos.com", "ttwstatic.com"}},
	{"instagram", "Instagram", "Social", []string{"instagram.com", "cdninstagram.com", "ig.me"}},
	{"facebook", "Facebook", "Social", []string{"facebook.com", "facebook.net", "fbcdn.net", "fb.com", "fb.me", "fbsbx.com"}},
	{"snapchat", "Snapchat", "Social", []string{"snapchat.com", "sc-cdn.net", "sc-static.net", "snapkit.co", "snap-dev.net"}},
	{"x", "X (Twitter)", "Social", []string{"twitter.com", "x.com", "twimg.com", "t.co"}},
	{"threads", "Threads", "Social", []string{"threads.net", "threads.com"}},
	{"reddit", "Reddit", "Social", []string{"reddit.com", "redd.it", "redditmedia.com", "redditstatic.com"}},
	{"pinterest", "Pinterest", "Social", []string{"pinterest.com", "pinimg.com"}},
	{"tumblr", "Tumblr", "Social", []string{"tumblr.com"}},
	{"bereal", "BeReal", "Social", []string{"bereal.com", "bere.al"}},
	{"linkedin", "LinkedIn", "Social", []string{"linkedin.com", "licdn.com"}},
	// messaging
	{"whatsapp", "WhatsApp", "Messaging", []string{"whatsapp.com", "whatsapp.net", "wa.me"}},
	{"messenger", "Messenger", "Messaging", []string{"messenger.com", "m.me"}},
	{"discord", "Discord", "Messaging", []string{"discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media"}},
	{"telegram", "Telegram", "Messaging", []string{"telegram.org", "t.me", "telegram.me"}},
	{"signal", "Signal", "Messaging", []string{"signal.org", "whispersystems.org"}},
	{"wechat", "WeChat", "Messaging", []string{"wechat.com"}},
	// video and music
	{"youtube", "YouTube", "Video", []string{"youtube.com", "googlevideo.com", "ytimg.com", "youtu.be", "youtube-nocookie.com"}},
	{"netflix", "Netflix", "Video", []string{"netflix.com", "netflix.net", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net"}},
	{"disneyplus", "Disney+", "Video", []string{"disneyplus.com", "disney-plus.net", "bamgrid.com", "dssott.com"}},
	{"primevideo", "Prime Video", "Video", []string{"primevideo.com", "aiv-cdn.net", "aiv-delivery.net"}},
	{"max", "Max", "Video", []string{"max.com", "hbomax.com", "hbo.com"}},
	{"hulu", "Hulu", "Video", []string{"hulu.com", "hulustream.com", "huluim.com"}},
	{"twitch", "Twitch", "Video", []string{"twitch.tv", "ttvnw.net", "jtvnw.net"}},
	{"kick", "Kick", "Video", []string{"kick.com"}},
	{"spotify", "Spotify", "Music", []string{"spotify.com", "scdn.co", "spotifycdn.com"}},
	// games
	{"roblox", "Roblox", "Games", []string{"roblox.com", "rbxcdn.com", "rbx.com", "robloxlabs.com"}},
	{"fortnite", "Fortnite / Epic Games", "Games", []string{"fortnite.com", "epicgames.com", "epicgames.dev", "unrealengine.com"}},
	{"minecraft", "Minecraft", "Games", []string{"minecraft.net", "mojang.com", "minecraftservices.com"}},
	{"steam", "Steam", "Games", []string{"steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamserver.net"}},
	{"playstation", "PlayStation Network", "Games", []string{"playstation.com", "playstation.net", "sonyentertainmentnetwork.com"}},
	{"xbox", "Xbox Live", "Games", []string{"xbox.com", "xboxlive.com", "xboxservices.com"}},
	{"nintendo", "Nintendo", "Games", []string{"nintendo.net", "nintendo.com"}},
	{"supercell", "Supercell (Clash, Brawl Stars)", "Games", []string{"supercell.com", "brawlstars.com", "clashroyale.com", "clashofclans.com"}},
	{"riot", "Riot Games (League, Valorant)", "Games", []string{"riotgames.com", "leagueoflegends.com", "riotcdn.net", "playvalorant.com"}},
	{"callofduty", "Call of Duty / Activision", "Games", []string{"callofduty.com", "activision.com", "demonware.net"}},
	{"blizzard", "Blizzard / Battle.net", "Games", []string{"blizzard.com", "battle.net"}},
	{"ea", "EA", "Games", []string{"ea.com"}},
	{"pokemongo", "Pokémon GO", "Games", []string{"nianticlabs.com", "pokemongolive.com"}},
	{"king", "Candy Crush (King)", "Games", []string{"king.com"}},
	{"amongus", "Among Us", "Games", []string{"innersloth.com"}},
	// AI chat
	{"chatgpt", "ChatGPT", "AI", []string{"chatgpt.com", "openai.com", "oaiusercontent.com", "oaistatic.com"}},
	{"claude", "Claude", "AI", []string{"claude.ai", "anthropic.com"}},
	{"characterai", "Character.AI", "AI", []string{"character.ai", "characterai.io"}},
	// dating
	{"tinder", "Tinder", "Dating", []string{"tinder.com", "gotinder.com"}},
	{"bumble", "Bumble", "Dating", []string{"bumble.com"}},
	// shopping
	{"temu", "Temu", "Shopping", []string{"temu.com", "kwcdn.com"}},
	{"shein", "Shein", "Shopping", []string{"shein.com", "ltwebstatic.com"}},
	// learning and calls
	{"duolingo", "Duolingo", "Learning", []string{"duolingo.com"}},
	{"zoom", "Zoom", "Calls", []string{"zoom.us", "zoom.com"}},
}

var byID = map[string]App{}
var byRoot = map[string]string{}

func init() {
	for _, a := range Catalog {
		byID[a.ID] = a
		for _, r := range a.Roots {
			byRoot[r] = a.ID
		}
	}
}

// Get finds an app by ID.
func Get(id string) (App, bool) { a, ok := byID[id]; return a, ok }

// Of names the app a domain belongs to ("" when none): the domain itself or any parent.
func Of(domain string) string {
	d := strings.TrimSuffix(strings.ToLower(domain), ".")
	for d != "" {
		if id, ok := byRoot[d]; ok {
			return id
		}
		i := strings.IndexByte(d, '.')
		if i < 0 {
			break
		}
		d = d[i+1:]
	}
	return ""
}

// Pairs lists every (root, app) pair, for SQL.
func Pairs() [][2]string {
	var out [][2]string
	for _, a := range Catalog {
		for _, r := range a.Roots {
			out = append(out, [2]string{r, a.ID})
		}
	}
	return out
}
