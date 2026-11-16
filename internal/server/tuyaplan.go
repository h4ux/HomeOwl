package server

import (
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

// tuyaPlan is how a house is drawn on the Tuya page's Plan, Dollhouse and Rooms views: where each room sits on the
// floor plan (a 1000-wide drawing), its floor, whether it's outside, its look, and where each switch sits in its
// room ("<device id>/<switch code>" → x, y as fractions of the room). Rooms are keyed by name, as Smart Life has them.
type tuyaPlan struct {
	Rooms map[string]tuyaPlanRoom `json:"rooms,omitempty"`
	Dots  map[string][2]float64   `json:"dots,omitempty"`
}

type tuyaPlanRoom struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Floor int     `json:"floor,omitempty"` // 0 = ground; 1, 2 up; -1 basement
	Out   *bool   `json:"out,omitempty"`   // outside (garden, pool, parking); unset: the page guesses from the name
	Look  string  `json:"look,omitempty"`  // a preset background, or "photo" (tuya-bg/<house>/<room>)
	Photo int64   `json:"photo,omitempty"`
}

// tuyaSetPlan saves a house's drawing as the page sends it, checked and clamped. Room looks are kept: they're set
// one room at a time (tuyaSetRoomLook).
func (a *App) tuyaSetPlan(id string, raw any) error {
	var p tuyaPlan
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &p); err != nil {
		return errors.New("that layout can't be read")
	}
	if len(p.Rooms) > 64 || len(p.Dots) > 1024 {
		return errors.New("that layout is too big")
	}
	clamp := func(v, lo, hi float64) float64 {
		if math.IsNaN(v) {
			return lo
		}
		return math.Max(lo, math.Min(hi, v))
	}
	for k, r := range p.Rooms {
		if k == "" || utf8.RuneCountInString(k) > 60 {
			return errors.New("a room's name is too long")
		}
		if r.W != 0 || r.H != 0 { // 0 × 0: not placed yet (only its floor or outside set); the page lays it out
			r.X, r.Y = clamp(r.X, 0, 990), clamp(r.Y, 0, 2000)
			r.W, r.H = clamp(r.W, 40, 1000), clamp(r.H, 40, 1000)
		}
		r.Floor = int(clamp(float64(r.Floor), -2, 6))
		r.Look, r.Photo = "", 0
		p.Rooms[k] = r
	}
	for k, d := range p.Dots {
		if k == "" || len(k) > 120 {
			return errors.New("a switch's name is too long")
		}
		p.Dots[k] = [2]float64{clamp(d[0], 0, 1), clamp(d[1], 0, 1)}
	}
	st := a.tuya
	st.mu.Lock()
	defer st.mu.Unlock()
	i := st.home(id)
	if i < 0 {
		return errors.New("that house is gone")
	}
	if old := st.Homes[i].Plan; old != nil {
		for k, r := range p.Rooms {
			if o, ok := old.Rooms[k]; ok {
				r.Look, r.Photo = o.Look, o.Photo
				p.Rooms[k] = r
			}
		}
	}
	if len(p.Rooms) == 0 && len(p.Dots) == 0 {
		st.Homes[i].Plan = nil // back to the automatic layout
	} else {
		st.Homes[i].Plan = &p
	}
	st.saveLocked()
	return nil
}

// tuyaSetRoomLook sets one room's background: a preset, or a photo (kept like a house's, under "<house>/<room>").
func (a *App) tuyaSetRoomLook(id, room, look, photo string) error {
	if room == "" || utf8.RuneCountInString(room) > 60 {
		return errors.New("pick a room")
	}
	st := a.tuya
	st.mu.Lock()
	i := st.home(id)
	st.mu.Unlock()
	if i < 0 {
		return errors.New("that house is gone")
	}
	if err := a.tuyaSavePhoto(id+"/"+room, look, photo); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if i = st.home(id); i < 0 {
		return errors.New("that house is gone")
	}
	p := st.Homes[i].Plan
	if p == nil {
		p = &tuyaPlan{}
		st.Homes[i].Plan = p
	}
	if p.Rooms == nil {
		p.Rooms = map[string]tuyaPlanRoom{}
	}
	r, ok := p.Rooms[room]
	if !ok { // not placed yet: the page lays it out automatically; W = 0 says so
		r = tuyaPlanRoom{}
	}
	r.Look, r.Photo = look, 0
	if look == "photo" {
		r.Photo = time.Now().Unix()
	}
	p.Rooms[room] = r
	st.saveLocked()
	return nil
}
