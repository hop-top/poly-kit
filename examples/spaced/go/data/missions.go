// Package data contains hardcoded mission, vehicle, and competitor data for spaced.
package data

import "strings"

// Outcome represents mission result.
type Outcome string

const (
	OutcomeSuccess Outcome = "SUCCESS"
	OutcomeRUD     Outcome = "RUD*"
	OutcomePending Outcome = "PENDING"
	OutcomePartial Outcome = "PARTIAL"
)

// Mission holds all known facts and satirical metadata about a SpaceX launch.
type Mission struct {
	ID          string
	Name        string
	Vehicle     string
	Date        string
	Orbit       string
	Payload     string
	Outcome     Outcome
	MarketMoods []string // pool; one picked at runtime
	ElonQuotes  []string // pool; one picked at runtime
	Notes       []string // pool; one picked at runtime
	Assessments []string // pool; one picked at runtime
	// Inspect-only fields
	Passenger string
	Playing   string
	Location  string
}

// Missions is the canonical mission catalog.
var Missions = []Mission{
	{
		ID:        "starman",
		Name:      "Starman",
		Vehicle:   "Falcon Heavy (debut)",
		Date:      "2018-02-06",
		Orbit:     "Heliocentric (Mars-crossing)",
		Payload:   "One (1) cherry red Tesla Roadster",
		Outcome:   OutcomeSuccess,
		Passenger: "Starman (mannequin, unresponsive to calls)",
		Playing:   "Space Oddity — David Bowie, on loop, forever",
		Location:  "Somewhere past Mars. We stopped tracking.",
		MarketMoods: []string{
			"📈 briefly",
			"📈 until the side boosters landed simultaneously",
			"🚀 Elon tweeted 17 times",
			"💎 meme stonks up",
		},
		ElonQuotes: []string{
			`"I had this image of just a giant explosion"`,
			`"It's silly and fun, but silly and fun things are important"`,
			`"Midnight Cherry Tesla Roadster playing Space Oddity"`,
		},
		Notes: []string{
			"The core booster missed the drone ship by 100m. Nobody talks about this.",
			"The car has exceeded its 36,000 mile warranty by roughly 5 billion miles.",
			"Starman has now completed ~4 laps around the sun. Still no calls back.",
		},
		Assessments: []string{
			"Unhinged. Worked perfectly.",
			"Peak Elon. Cannot be replicated.",
			"A car is orbiting the sun. This is fine.",
		},
	},
	{
		ID:      "crew-dragon-demo2",
		Name:    "Crew Dragon Demo2",
		Vehicle: "Falcon 9",
		Date:    "2020-05-30",
		Orbit:   "Low Earth Orbit (ISS)",
		Payload: "Bob Behnken, Doug Hurley",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"🚀 Elon tweeted 47 times",
			"🇺🇸 nationalism briefly peaked",
			"📈 NASA exhaled for first time in 9 years",
			"🎉 Boeing quietly wept",
		},
		ElonQuotes: []string{
			`"I'm feeling quite emotional"`,
			`"America is back in the business of launching astronauts"`,
			`"This is hopefully the first of many"`,
		},
		Notes: []string{
			"First crewed orbital flight from US soil since Shuttle retired in 2011.",
			"Bob and Doug wore touchscreen-compatible gloves. Peak 2020 engineering.",
			"Boeing was supposed to do this. They are... still working on it.",
		},
		Assessments: []string{
			"Historic. Also extremely embarrassing for Boeing.",
			"America returned to human spaceflight. Boeing did not.",
			"NASA cried. Boeing filed another request for schedule extension.",
		},
	},
	{
		ID:      "sn8",
		Name:    "SN8",
		Vehicle: "Starship",
		Date:    "2020-12-09",
		Orbit:   "Suborbital (15km hop)",
		Payload: "None (test article)",
		Outcome: OutcomeRUD,
		MarketMoods: []string{
			`🔥 "nominal"`,
			"🔥 Elon said it was 'awesome' before it exploded",
			"🔥 FAA had questions. Many questions.",
			"📈 SpaceX fans cheered the fireball",
		},
		ElonQuotes: []string{
			`"Fuel header tank pressure was low during landing burn, causing touchdown velocity to be high & RUD, but we got all the data we needed!"`,
			`"Mars, here we come!!"`,
			`"Success is not guaranteed, but excitement is"`,
		},
		Notes: []string{
			"RUD = Rapid Unscheduled Disassembly. This is official SpaceX terminology.",
			"The vehicle executed a breathtaking bellyflop maneuver, then exploded beautifully.",
			"SpaceX declared it a success. The crater disagreed silently.",
		},
		Assessments: []string{
			"Exploded. Also incredible.",
			"A 50-meter rocket did a bellyflop and almost stuck the landing. RUD after.",
			"Technically failed. Culturally a triumph. SpaceX math.",
		},
	},
	{
		ID:      "sn15",
		Name:    "SN15",
		Vehicle: "Starship",
		Date:    "2021-05-05",
		Orbit:   "Suborbital (10km hop)",
		Payload: "None (test article)",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"🔥 relief",
			"📈 SN8-SN14 fans vindicated",
			"🚀 Boca Chica cheered, cows unimpressed",
			"🎉 Elon tweeted 'it landed!!'",
		},
		ElonQuotes: []string{
			`"It landed!!"`,
			`"Starship will one day carry people to Mars"`,
			`"Congrats SpaceX team on SN15 landing!!"`,
		},
		Notes: []string{
			"SN8 through SN14 did not survive. SN15 survived. Progress is non-linear.",
			"There was a small fire at the base post-landing. SpaceX called it 'residual'.",
			"The flight path over Boca Chica Village prompted another FAA waiver application.",
		},
		Assessments: []string{
			"Finally landed. Worth the craters.",
			"5 attempts, 1 survivor. SpaceX considers this efficient.",
			"A+ for persistence. B for property damage minimization.",
		},
	},
	{
		ID:      "inspiration4",
		Name:    "Inspiration4",
		Vehicle: "Falcon 9",
		Date:    "2021-09-15",
		Orbit:   "Low Earth Orbit (higher than ISS, ~585km)",
		Payload: "Jared Isaacman, Hayley Arceneaux, Sian Proctor, Chris Sembroski",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"📈 Netflix already filming",
			"💰 Billionaire buys entire rocket",
			"🌍 Higher than ISS, lower than our expectations for society",
			"🚀 Hayley Arceneaux made history (youngest American in orbit)",
		},
		ElonQuotes: []string{
			`"The civilian space age has begun"`,
			`"It's incredibly inspiring"`,
			`"This is just the beginning"`,
		},
		Notes: []string{
			"First all-civilian orbital crew. Hayley Arceneaux was a cancer survivor + physician assistant.",
			"Jared Isaacman paid an undisclosed amount. Estimated: a lot.",
			"They had a cupola toilet view. First orbital lavatory with a view.",
		},
		Assessments: []string{
			"Billionaire tourism begins. At least they brought a nurse.",
			"Historic for accessibility. Also, there is a Netflix series.",
			"Genuinely moving. Also: cupola toilet.",
		},
	},
	{
		ID:      "ift1",
		Name:    "OFT-2 (IFT-1)",
		Vehicle: "Starship",
		Date:    "2023-04-20",
		Orbit:   "Suborbital (planned)",
		Payload: "None",
		Outcome: OutcomeRUD,
		MarketMoods: []string{
			"🔥 'It's not a failure, it's a learning experience'",
			"📰 FAA had 63 corrective actions",
			"🌪️ Concrete dust cloud visible from Mexico",
			"🔥 Launch mount survived. Barely. The pad did not.",
		},
		ElonQuotes: []string{
			`"Success is not binary"`,
			`"Exciting test launch of Starship!"`,
			`"Learned a lot for next test launch"`,
		},
		Notes: []string{
			"The launch pad was destroyed. SpaceX had not installed the flame deflector yet.",
			"The vehicle tumbled before the flight termination system activated.",
			"FAA issued 63 corrective actions before the next launch was permitted.",
		},
		Assessments: []string{
			"RUD²: vehicle AND pad. New personal record.",
			"Cleared the pad. Did not clear much else.",
			"FAA was not happy. Concrete chunks were not happy. We learned things.",
		},
	},
	{
		ID:      "ift2",
		Name:    "IFT-2",
		Vehicle: "Starship",
		Date:    "2023-11-18",
		Orbit:   "Suborbital",
		Payload: "None",
		Outcome: OutcomeRUD,
		MarketMoods: []string{
			"📈 Hot staging worked!",
			"🔥 Then it exploded anyway",
			"🤷 Further than IFT-1 though",
			"📰 FAA says 'we are reviewing'",
		},
		ElonQuotes: []string{
			`"Got much further this time"`,
			`"Each iteration we learn more"`,
			`"This is how rockets get built"`,
		},
		Notes: []string{
			"Successfully separated stages using hot-staging. Then lost telemetry. Then FTS.",
			"Stage 2 reached higher altitude than IFT-1 before self-destructing over Gulf.",
			"Stage 1 also eventually lost and destroyed. A productive day.",
		},
		Assessments: []string{
			"More exploded than IFT-1, further into the flight. Progress.",
			"Hot staging: A+. Staying in one piece: incomplete.",
			"Two explosions, one successful stage separation. Mixed bag.",
		},
	},
	{
		ID:      "ift3",
		Name:    "IFT-3",
		Vehicle: "Starship",
		Date:    "2024-03-14",
		Orbit:   "Near-orbital",
		Payload: "None",
		Outcome: OutcomeRUD,
		MarketMoods: []string{
			"📈 Survived reentry... mostly",
			"🔥 Lost contact during reentry. Again.",
			"🌊 Indian Ocean received an unexpected delivery",
			"📰 FCC asked about debris field",
		},
		ElonQuotes: []string{
			`"Starship reached orbital velocity"`,
			`"Reentry plasma blackout as expected"`,
			`"Another successful test flight"`,
		},
		Notes: []string{
			"Ship survived reentry but was lost before splashdown. Close.",
			"Super Heavy performed the 'boostback burn'. Did not return to pad.",
			"Reached orbital velocity. Did not reach 'surviving' velocity.",
		},
		Assessments: []string{
			"Got to space. Lost in space. Progress.",
			"Near-orbital RUD. Better than orbital RUD by most metrics.",
			"If 'surviving until splashdown' was not in scope, success.",
		},
	},
	{
		ID:      "ift4",
		Name:    "IFT-4",
		Vehicle: "Starship",
		Date:    "2024-06-06",
		Orbit:   "Near-orbital",
		Payload: "None",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"🎉 Both vehicles recovered",
			"📈 Elon very happy",
			"🚀 NASA quietly relieved (Artemis depends on this)",
			"📰 SpaceX PR machine activated",
		},
		ElonQuotes: []string{
			`"This is incredibly exciting"`,
			`"We have confirmation of soft splashdown"`,
			`"Starship is real"`,
		},
		Notes: []string{
			"First time both Super Heavy and Ship survived to planned endpoints.",
			"Ship survived reentry and completed controlled ocean entry.",
			"No longer just exploding things. A new era.",
		},
		Assessments: []string{
			"Nobody exploded. This is the new bar.",
			"Milestone: full mission profile without RUD.",
			"IFT-1 through IFT-3 were 'learning'. This was passing.",
		},
	},
	{
		ID:      "ift5",
		Name:    "IFT-5",
		Vehicle: "Starship",
		Date:    "2024-10-13",
		Orbit:   "Near-orbital",
		Payload: "None",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"🦾 Mechazilla caught the booster!",
			"🤯 Chopstick arms caught a 70-meter rocket",
			"📈 Physics confirmed by giant robot arms",
			"🔥 Everyone screamed",
		},
		ElonQuotes: []string{
			`"The tower has caught the rocket!!"`,
			`"This is one of the greatest moments in human history"`,
			`"Mechazilla works"`,
		},
		Notes: []string{
			"Mechazilla (launch/catch tower) caught Super Heavy with mechanical arms.",
			"A 70-meter, 200-ton booster was caught mid-air by chopstick arms. This happened.",
			"The crowd at Starbase lost its collective mind. Understandably.",
		},
		Assessments: []string{
			"A robot caught a rocket. Peak timeline.",
			"Chopsticks > landing legs. SpaceX has proven it.",
			"Objectively one of the coolest things ever attempted and succeeded.",
		},
	},
	{
		ID:      "ift6",
		Name:    "IFT-6",
		Vehicle: "Starship",
		Date:    "2025-01-16",
		Orbit:   "Near-orbital",
		Payload: "None",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"🦾 Mechazilla caught it again!",
			"📈 Nominal is the new normal",
			"🚀 Ship controlled splashdown confirmed",
			"🛰️ Artemis III might actually happen",
		},
		ElonQuotes: []string{
			`"Starship is ready for operational missions"`,
			`"Full reusability achieved"`,
			`"To the Moon"`,
		},
		Notes: []string{
			"Second successful Mechazilla catch. Becoming routine. Somehow.",
			"Ship executed precision ocean entry with telemetry maintained throughout.",
			"NASA's Human Landing System contract begins to look slightly less absurd.",
		},
		Assessments: []string{
			"Routine spaceflight is not routine but this is starting to look routine.",
			"Full reusability stack demonstrated twice. Still hard to believe.",
			"Catching rockets with robot arms is now boring. SpaceX won.",
		},
	},
	{
		ID:      "doge-1",
		Name:    "DOGE-1",
		Vehicle: "Falcon 9",
		Date:    "2021-05-29",
		Orbit:   "Translunar (planned)",
		Payload: "Geometric Energy Corporation cubesat",
		Outcome: OutcomePending,
		MarketMoods: []string{
			"🐶 First crypto-funded mission",
			"🌕 Doge to the moon, literally",
			"💎 Retail investors felt vindicated",
			"📰 Financial press confused",
		},
		ElonQuotes: []string{
			`"To the moon!!"`,
			`"Literally"`,
			`"Much mission. Very orbit."`,
		},
		Notes: []string{
			"First commercial lunar mission funded entirely by Dogecoin.",
			"The payload is a 40kg cubesat. The joke is a 100% joke that became real.",
			"DOGE-1 — yes, the government department named after the meme named after the mission. Timeline.",
		},
		Assessments: []string{
			"A meme coin funded a moon mission. We live in a simulation.",
			"The mission is real. The currency is real. The irony is overwhelming.",
			"Geometric Energy Corporation. Sure. Why not.",
		},
	},
	{
		ID:      "crs-1",
		Name:    "CRS-1",
		Vehicle: "Falcon 9",
		Date:    "2012-10-08",
		Orbit:   "Low Earth Orbit (ISS)",
		Payload: "Dragon cargo (400kg supplies)",
		Outcome: OutcomePartial,
		MarketMoods: []string{
			"📦 ISS got its groceries",
			"⚠️ Secondary payload lost due to engine anomaly",
			"📈 First commercial resupply mission to ISS",
			"🎉 NASA exhaled slightly",
		},
		ElonQuotes: []string{
			`"This is a huge milestone"`,
			`"Commercial spaceflight is here to stay"`,
			`"We will learn from the engine anomaly"`,
		},
		Notes: []string{
			"First operational Dragon resupply mission. One Falcon 9 engine failed at T+1:19.",
			"Primary mission (ISS delivery) succeeded. Secondary cubesat lost.",
			"NASA called it acceptable. SpaceX fixed the engine issue.",
		},
		Assessments: []string{
			"Partial success. ISS fed. Secondary payload sacrificed to the mission gods.",
			"Nine engines, one failed, ISS still got its coffee. Good margins.",
			"First of many CRS missions. The engine anomaly is a footnote.",
		},
	},
	{
		ID:      "gps-iii",
		Name:    "GPS III SV01",
		Vehicle: "Falcon 9",
		Date:    "2018-12-23",
		Orbit:   "Medium Earth Orbit",
		Payload: "Lockheed Martin GPS III satellite",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"🛰️ US Space Force getting a GPS upgrade",
			"📍 You will have 3x better accuracy eventually",
			"🎄 Christmas Eve Eve launch",
			"📈 Lockheed Martin applauded",
		},
		ElonQuotes: []string{
			`"Falcon 9 is the most reliable rocket in history"`,
			`"GPS III expands US space capabilities"`,
			`"Proud to support national security"`,
		},
		Notes: []string{
			"First GPS III satellite. More accurate, harder to jam, longer lifespan.",
			"No landing attempt on this mission — payload orbit requirements precluded recovery.",
			"US Space Force (before it was official) bought this slot.",
		},
		Assessments: []string{
			"Clean. Professional. No explosions. SpaceX can do boring.",
			"Government customer, on time, on orbit. Textbook.",
			"The most historically significant launch nobody talks about. You use GPS.",
		},
	},
	{
		ID:      "starlink-v1",
		Name:    "Starlink v1.0 L1",
		Vehicle: "Falcon 9",
		Date:    "2019-11-11",
		Orbit:   "Low Earth Orbit (550km)",
		Payload: "60 Starlink satellites",
		Outcome: OutcomeSuccess,
		MarketMoods: []string{
			"📡 Astronomers upset",
			"🌐 Global internet, eventually",
			"📈 60 satellites at once, like nothing",
			"🔭 ITU frequency filing madness begins",
		},
		ElonQuotes: []string{
			`"Starlink will provide affordable internet to underserved areas"`,
			`"This is critical infrastructure for Earth"`,
			`"We'll put up thousands"`,
		},
		Notes: []string{
			"First batch of operational Starlink v1 sats. They've since launched thousands more.",
			"Astronomers immediately noticed bright streaks in telescope images.",
			"SpaceX has since added sun visors. Astronomers remain skeptical.",
		},
		Assessments: []string{
			"Changed the internet. Also changed astrophotography. Negatively.",
			"60 satellites, one rocket, routine flight. The new normal starts here.",
			"Useful for underserved regions. Annoying for anyone with a telescope.",
		},
	},
	{
		ID:          "time",
		Name:        "time",
		Vehicle:     "—",
		Date:        "—",
		Orbit:       "—",
		Payload:     "—",
		Outcome:     OutcomePending,
		MarketMoods: []string{"⏳"},
		ElonQuotes:  []string{`""`},
		Notes:       []string{""},
		Assessments: []string{""},
	},
}

// FindMission returns the mission matching id (slug or name, case-insensitive).
func FindMission(id string) (*Mission, bool) {
	id = strings.ToLower(strings.ReplaceAll(id, " ", "-"))
	for i := range Missions {
		m := &Missions[i]
		if strings.ToLower(m.ID) == id || strings.ToLower(strings.ReplaceAll(m.Name, " ", "-")) == id {
			return m, true
		}
	}
	return nil, false
}

// SearchMissions returns missions whose Name, Vehicle, or Payload contains query (case-insensitive).
func SearchMissions(query string) []Mission {
	q := strings.ToLower(query)
	var out []Mission
	for _, m := range Missions {
		if m.ID == "time" {
			continue
		}
		if strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.Vehicle), q) ||
			strings.Contains(strings.ToLower(m.Payload), q) {
			out = append(out, m)
		}
	}
	return out
}

func init() {
	// Remove placeholder entry from public slice.
	filtered := Missions[:0]
	for _, m := range Missions {
		if m.ID != "time" {
			filtered = append(filtered, m)
		}
	}
	Missions = filtered
}
