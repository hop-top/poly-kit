package data

import "strings"

// Vehicle holds hardcoded data about SpaceX launch vehicles.
type Vehicle struct {
	ID          string
	Name        string
	Type        string
	Status      string
	FirstFlight string
	Height      string
	Payload     string
	Landings    int
	Flights     int
	Systems     map[string]VehicleSystem
	Notes       []string
	ElonQuotes  []string
}

// VehicleSystem describes one inspectable subsystem.
type VehicleSystem struct {
	Name        string
	Status      string
	Description string
}

// Vehicles is the canonical vehicle catalog.
var Vehicles = []Vehicle{
	{
		ID:          "falcon-9",
		Name:        "Falcon 9",
		Type:        "Orbital Rocket",
		Status:      "OPERATIONAL",
		FirstFlight: "2010-06-04",
		Height:      "70m",
		Payload:     "22,800 kg to LEO",
		Landings:    300,
		Flights:     320,
		Systems: map[string]VehicleSystem{
			"merlin":  {Name: "Merlin Engines", Status: "NOMINAL", Description: "9x Merlin 1D on stage 1; 1x Merlin Vacuum on stage 2"},
			"fairing": {Name: "Payload Fairing", Status: "REUSED", Description: "5.2m diameter; recovered with nets on Mr Steven (RIP) and now ships"},
			"legs":    {Name: "Landing Legs", Status: "DEPLOYED", Description: "4x carbon-fiber/aluminum legs. Fold out. Occasionally fail."},
			"grid":    {Name: "Grid Fins", Status: "NOMINAL", Description: "Titanium. Added after one melted. Lesson learned."},
		},
		Notes: []string{
			"Most flown rocket in history. Every booster has a personal landing record.",
			"Some boosters have flown 20+ times. The Wright Flyer flew 4 times. Just saying.",
			"Block 5 is the final version. SpaceX said this 3 versions ago.",
		},
		ElonQuotes: []string{
			`"Falcon 9 is the most reliable rocket in history"`,
			`"We can fly Falcon 9 forever"`,
			`"Reliability is more important than innovation sometimes"`,
		},
	},
	{
		ID:          "falcon-heavy",
		Name:        "Falcon Heavy",
		Type:        "Heavy-Lift Orbital Rocket",
		Status:      "OPERATIONAL",
		FirstFlight: "2018-02-06",
		Height:      "70m",
		Payload:     "63,800 kg to LEO",
		Landings:    18,
		Flights:     10,
		Systems: map[string]VehicleSystem{
			"cores":   {Name: "Side Boosters", Status: "REUSED", Description: "Two Falcon 9 boosters bolted on. Simple and effective."},
			"central": {Name: "Center Core", Status: "OCCASIONALLY_LOST", Description: "The middle core that sometimes misses the drone ship by 100m"},
			"merlin":  {Name: "Merlin Engines", Status: "NOMINAL", Description: "27x Merlin 1D at liftoff. More than the Saturn V. For a while."},
		},
		Notes: []string{
			"Second most powerful rocket currently operational. Number one: the title of this note.",
			"The center core has a harder landing trajectory. It shows.",
			"Side booster simultaneous landings look fake. They are not.",
		},
		ElonQuotes: []string{
			`"It's the most powerful rocket in the world"`,
			`"Falcon Heavy will open up a new era of space exploration"`,
			`"I had this image of just a giant explosion on the pad"`,
		},
	},
	{
		ID:          "starship",
		Name:        "Starship",
		Type:        "Super-Heavy-Lift Orbital Rocket",
		Status:      "TESTING",
		FirstFlight: "2023-04-20",
		Height:      "121m",
		Payload:     "150,000 kg to LEO (fully reusable: 100t)",
		Landings:    4,
		Flights:     6,
		Systems: map[string]VehicleSystem{
			"raptor":     {Name: "Raptor Engines", Status: "IMPROVING", Description: "33x Raptor on Super Heavy, 6x on Ship. Some have exploded. Many haven't."},
			"mechazilla": {Name: "Mechazilla Arms", Status: "OPERATIONAL", Description: "Giant robot chopstick arms that catch a 70m booster. Somehow works."},
			"heatshield": {Name: "Heat Shield Tiles", Status: "LOSSY", Description: "Hexagonal tiles. Some fall off during reentry. SpaceX is aware."},
			"propellant": {Name: "Propellant Transfer", Status: "DEMO_PENDING", Description: "Critical for Artemis. Not yet demonstrated. NASA is patient. Allegedly."},
		},
		Notes: []string{
			"Tallest rocket ever flown. Also most exploded rocket still in active development.",
			"Mechazilla catching Super Heavy is still the most insane thing SpaceX has done.",
			"NASA's Artemis Moon lander depends on Starship. This is either inspiring or alarming.",
		},
		ElonQuotes: []string{
			`"Starship is the most complex engineering project in human history"`,
			`"We will take humans to Mars in this decade"`,
			`"Starship will make humanity multi-planetary"`,
		},
	},
	{
		ID:          "dragon-crew",
		Name:        "Dragon (Crew)",
		Type:        "Crewed Spacecraft",
		Status:      "OPERATIONAL",
		FirstFlight: "2020-05-30",
		Height:      "8.1m",
		Payload:     "4 crew + cargo",
		Landings:    12,
		Flights:     14,
		Systems: map[string]VehicleSystem{
			"life-support": {Name: "Life Support", Status: "NOMINAL", Description: "ECLSS. Keeps astronauts alive. Has done so every time."},
			"abort":        {Name: "Launch Escape", Status: "STANDBY", Description: "SuperDraco abort motors. Demonstrated 2019. Hope to never use operationally."},
			"splashdown":   {Name: "Splashdown System", Status: "NOMINAL", Description: "4 main parachutes. Occasionally dramatic. Always successful."},
		},
		Notes: []string{
			"First commercial spacecraft to carry NASA astronauts to ISS.",
			"Also carried Boeing's astronauts home when Starliner couldn't.",
			"Butch and Suni waited 8 months for Dragon to rescue them from Starliner.",
		},
		ElonQuotes: []string{
			`"Dragon has made commercial crew a reality"`,
			`"We are proud to serve NASA astronauts"`,
			`"Reliability is the priority for crewed missions"`,
		},
	},
	{
		ID:          "dragon-cargo",
		Name:        "Dragon (Cargo)",
		Type:        "Cargo Spacecraft",
		Status:      "OPERATIONAL",
		FirstFlight: "2012-10-08",
		Height:      "8.1m",
		Payload:     "6,000 kg to ISS",
		Landings:    28,
		Flights:     30,
		Systems: map[string]VehicleSystem{
			"trunk": {Name: "Unpressurized Trunk", Status: "NOMINAL", Description: "Carries external cargo like solar panels. Stays in orbit, then burns up."},
			"berth": {Name: "Common Berth Mechanism", Status: "NOMINAL", Description: "ISS docking adapter. Compatible with multiple ports."},
		},
		Notes: []string{
			"NASA's grocery delivery service since 2012. More reliable than DoorDash.",
			"SpaceX delivers 30 times per contract. Boeing is still filling out paperwork.",
			"Also reused. Some cargo capsules have flown 4+ missions.",
		},
		ElonQuotes: []string{
			`"Dragon is the workhorse of ISS resupply"`,
			`"Reusability transforms the economics of spaceflight"`,
			`"Commercial cargo is now mature technology"`,
		},
	},
	{
		ID:          "mechazilla",
		Name:        "Starship Mechazilla",
		Type:        "Launch and Catch Tower",
		Status:      "OPERATIONAL",
		FirstFlight: "2024-10-13",
		Height:      "146m",
		Payload:     "Can catch a 70-meter rocket",
		Landings:    2,
		Flights:     0,
		Systems: map[string]VehicleSystem{
			"arms":   {Name: "Chopstick Arms", Status: "OPERATIONAL", Description: "Two massive mechanical arms. Officially 'Mechazilla'. Not a joke."},
			"launch": {Name: "Launch Clamps", Status: "NOMINAL", Description: "Hold 5,000-ton vehicle at ignition. Release on schedule. Usually."},
		},
		Notes: []string{
			"It's a robot that catches rockets. No further explanation should be needed.",
			"Named 'Mechazilla' internally. 'Mechazilla' is also what we call it.",
			"Has caught Super Heavy twice. Both times, everyone screamed.",
		},
		ElonQuotes: []string{
			`"The tower has caught the rocket!!"`,
			`"Mechazilla is the future of rocket reuse"`,
			`"No other company has done this or will for decades"`,
		},
	},
}

// FindVehicle returns the vehicle matching id (slug or name, case-insensitive).
func FindVehicle(id string) (*Vehicle, bool) {
	id = strings.ToLower(strings.ReplaceAll(id, " ", "-"))
	for i := range Vehicles {
		v := &Vehicles[i]
		if strings.ToLower(v.ID) == id ||
			strings.ToLower(strings.ReplaceAll(v.Name, " ", "-")) == id {
			return v, true
		}
	}
	return nil, false
}
