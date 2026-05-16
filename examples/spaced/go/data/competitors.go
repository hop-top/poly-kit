package data

import "strings"

// Competitor holds comparison data for a SpaceX rival.
type Competitor struct {
	ID         string
	Name       string
	Founded    string
	CEO        string
	Status     string
	Highlights []string
	Metrics    map[string]CompetitorMetric
	Verdict    string
	ElonQuotes []string
}

// CompetitorMetric is one comparable data point.
type CompetitorMetric struct {
	Label  string
	SpaceX string
	Them   string
	Winner string
}

// Daemon tracks an ongoing controversy with media references.
type Daemon struct {
	ID          string
	Name        string
	Since       string // date string YYYY-MM-DD
	Status      string
	Description string
	References  []DaemonRef
	StopMessage string // shown on daemon stop
}

// DaemonRef is one media citation.
type DaemonRef struct {
	Source  string
	Author  string
	Date    string
	Summary string
}

// Competitors is the canonical competitor catalog.
var Competitors = []Competitor{
	{
		ID:      "boeing",
		Name:    "Boeing",
		Founded: "1916",
		CEO:     "Kelly Ortberg (2024–)",
		Status:  "STRUCTURALLY_DISAPPOINTED",
		Highlights: []string{
			"CST-100 Starliner: 8 years late, $1.5B over budget",
			"Stuck two astronauts at ISS for 8 months; SpaceX brought them home",
			"737 MAX grounded twice; brand in structural freefall",
			"Still has NASA contracts because aerospace has 2 vendors",
		},
		Metrics: map[string]CompetitorMetric{
			"reliability": {Label: "Rocket reliability", SpaceX: "~99%", Them: "33% (Starliner 2/3 flights had issues)", Winner: "SpaceX"},
			"cost":        {Label: "Cost per kg to LEO", SpaceX: "$2,700", Them: "$54,000 (SLS)", Winner: "SpaceX"},
			"schedule":    {Label: "Schedule adherence", SpaceX: "Occasional", Them: "Never", Winner: "SpaceX"},
			"astronauts":  {Label: "Astronauts stranded", SpaceX: "0", Them: "2 (Butch & Suni, 286 days)", Winner: "SpaceX"},
		},
		Verdict: "Once built the Moon rocket. Now can't finish a capsule started in 2014. History is cruel.",
		ElonQuotes: []string{
			`"Boeing is a very talented company that has had some execution challenges"`,
			`"We hope Starliner succeeds. Seriously. Competition is good."`,
		},
	},
	{
		ID:      "blue-origin",
		Name:    "Blue Origin",
		Founded: "2000",
		CEO:     "Dave Limp (2023–)",
		Status:  "ASCENDING_SLOWLY",
		Highlights: []string{
			"New Shepard: suborbital tourism; 25 flights, 0 orbital",
			"New Glenn: first orbital flight January 2025 (partial success)",
			"BE-4 engine: 5 years late; finally delivered to ULA",
			"Jeff Bezos founded it; sold Amazon stock to fund it",
		},
		Metrics: map[string]CompetitorMetric{
			"orbital": {Label: "Orbital missions flown", SpaceX: "300+", Them: "1", Winner: "SpaceX"},
			"crew":    {Label: "Orbital crew missions", SpaceX: "15+", Them: "0", Winner: "SpaceX"},
			"reuse":   {Label: "Reusable boosters", SpaceX: "~300 landings", Them: "15 (suborbital only)", Winner: "SpaceX"},
			"moon":    {Label: "Lunar lander (HLS)", SpaceX: "Starship (contracted)", Them: "Blue Moon (also contracted)", Winner: "Tie (for now)"},
		},
		Verdict: "Founded one year before SpaceX. Has been suborbital the whole time. Tortoise energy.",
		ElonQuotes: []string{
			`"Jeff Bezos' hobby"`,
			`"Blue Origin has not yet reached orbit. SpaceX reached orbit in 2008."`,
		},
	},
	{
		ID:      "virgin-galactic",
		Name:    "Virgin Galactic",
		Founded: "2004",
		CEO:     "Michael Colglazier → Aarav Shah",
		Status:  "TECHNICALLY_OPERATIONAL_ALLEGEDLY",
		Highlights: []string{
			"SpaceShipTwo: suborbital tourism at 86km (Kármán line: 100km)",
			"Branson flew in July 2021, 9 days before Bezos",
			"VSS Unity retired 2023; Delta class under development",
			"Stock: $20B peak market cap, now less than a Tesla Cybertruck",
		},
		Metrics: map[string]CompetitorMetric{
			"altitude":   {Label: "Max altitude", SpaceX: "~550km (ISS orbit)", Them: "86km (FAA calls it space)", Winner: "SpaceX"},
			"orbital":    {Label: "Orbital missions", SpaceX: "300+", Them: "0", Winner: "SpaceX"},
			"customers":  {Label: "Paying customers flown", SpaceX: "14+", Them: "6", Winner: "SpaceX"},
			"market-cap": {Label: "Market cap stability", SpaceX: "Private ($210B)", Them: "Volatile (≈$400M)", Winner: "SpaceX"},
		},
		Verdict: "Richard Branson is technically an astronaut by US standards. Elon finds this funny.",
		ElonQuotes: []string{
			`"Virgin Galactic is doing a great job with suborbital tourism"`,
			`"The market for suborbital is smaller than orbital. Much smaller."`,
		},
	},
	{
		ID:      "ula",
		Name:    "ULA (United Launch Alliance)",
		Founded: "2006",
		CEO:     "Tory Bruno",
		Status:  "TRANSITIONING",
		Highlights: []string{
			"Atlas V: 99 launches, 0 failures; retiring in 2024",
			"Vulcan Centaur: first flight January 2024 (Peregrine lander)",
			"Delta IV Heavy: retired 2024; most expensive expendable rocket",
			"BE-4 powered: waited 5 years for Blue Origin to deliver the engines",
		},
		Metrics: map[string]CompetitorMetric{
			"reliability": {Label: "Mission reliability", SpaceX: "~99%", Them: "100% (Atlas V)", Winner: "Tie (different era)"},
			"cost":        {Label: "Cost to LEO", SpaceX: "$2,700/kg", Them: "$14,000+/kg", Winner: "SpaceX"},
			"reuse":       {Label: "Reusable first stage", SpaceX: "Yes", Them: "No", Winner: "SpaceX"},
			"gov":         {Label: "Government market share", SpaceX: "Growing", Them: "Shrinking", Winner: "SpaceX"},
		},
		Verdict: "99 launches, 0 failures. A spotless record achieved by spending roughly infinity dollars.",
		ElonQuotes: []string{
			`"ULA builds great rockets. They're just expensive."`,
			`"Competition from ULA is healthy even if the cost curves don't overlap."`,
		},
	},
	{
		ID:      "roscosmos",
		Name:    "Roscosmos",
		Founded: "1992",
		CEO:     "Yury Borisov (2022–)",
		Status:  "SANCTIONS_APPLY",
		Highlights: []string{
			"Soyuz: most flown rocket in history (1966–present)",
			"Lost ISS taxi monopoly to SpaceX in 2020",
			"Luna-25: crashed into Moon in 2023 (first lunar mission in 47 years)",
			"ISS cooperation complicated by 2022 events; future uncertain",
		},
		Metrics: map[string]CompetitorMetric{
			"history": {Label: "Orbital missions (all-time)", SpaceX: "300+", Them: "3,000+ (since 1957)", Winner: "Roscosmos (history)"},
			"crew":    {Label: "Active crew vehicles", SpaceX: "Dragon", Them: "Soyuz", Winner: "Tie (both work)"},
			"cost":    {Label: "Seat cost to ISS", SpaceX: "$55M", Them: "$90M (pre-2020)", Winner: "SpaceX"},
			"moon":    {Label: "Recent lunar success", SpaceX: "N/A (not tried yet)", Them: "0/1 (crashed)", Winner: "SpaceX (by absence)"},
		},
		Verdict: "Built the first satellite, first human in space, and first spacewalk. Currently: complicated.",
		ElonQuotes: []string{
			`"Russia has incredible aerospace heritage"`,
			`"The current situation is unfortunate for international space cooperation"`,
		},
	},
}

// Daemons is the canonical daemon (controversy) registry.
var Daemons = []Daemon{
	{
		ID:     "funding-secured",
		Name:   "funding-secured",
		Since:  "2018-08-07",
		Status: "RUNNING",
		Description: "Elon Musk tweeted 'Am considering taking Tesla private at $420. Funding secured.' " +
			"The funding was not secured. The SEC was not amused. The tweet is still up.",
		References: []DaemonRef{
			{Source: "SEC Complaint", Author: "U.S. Securities and Exchange Commission", Date: "2018-09-27",
				Summary: "SEC charges Musk with securities fraud; settlement $20M each (Musk + Tesla)"},
			{Source: "New York Times", Author: "Kate Kelly, Emily Flitter", Date: "2018-08-16",
				Summary: "Inside story of the '420 tweet' and its aftermath"},
			{Source: "Reuters", Author: "Tom Hals, Jonathan Stempel", Date: "2022-01-11",
				Summary: "Judge rejects Musk bid to escape Tesla tweet SEC oversight, 4 years later"},
		},
		StopMessage: "You cannot stop funding-secured.\nThe SEC tried. It cost $40M and took 4 years.\nThe tweet is still up.",
	},
	{
		ID:     "twitter-acquisition-chaos",
		Name:   "twitter-acquisition-chaos",
		Since:  "2022-10-27",
		Status: "RUNNING",
		Description: "Elon Musk acquired Twitter for $44B, fired half the staff, rebranded it X, " +
			"restored banned accounts, and turned it into a case study in chaotic governance.",
		References: []DaemonRef{
			{Source: "Wall Street Journal", Author: "Alexa Corse, Sarah E. Needleman", Date: "2022-11-04",
				Summary: "Musk fires ~3,700 Twitter employees; chaotic first week documented"},
			{Source: "The Verge", Author: "Nilay Patel", Date: "2022-10-28",
				Summary: "'Extremely hardcore' memo; sink-or-swim ultimatum to remaining staff"},
			{Source: "Bloomberg", Author: "Mark Gurman, Kurt Wagner", Date: "2023-07-23",
				Summary: "Twitter rebranded to X; bird logo removed overnight"},
		},
		StopMessage: "You cannot stop twitter-acquisition-chaos.\nIt became X. Then X became a political platform.\n" +
			"Then the EU opened an investigation. Currently: still running.",
	},
	{
		ID:     "doge-conflict-of-interest",
		Name:   "doge-conflict-of-interest",
		Since:  "2025-01-20",
		Status: "RUNNING",
		Description: "Musk's Department of Government Efficiency (DOGE) operates under his direction " +
			"while SpaceX, Tesla, and X have combined billions in government contracts. Ethicists are concerned. " +
			"Musk is not.",
		References: []DaemonRef{
			{Source: "ProPublica", Author: "Isaac Arnsdorf, Josh Dawsey", Date: "2025-02-14",
				Summary: "DOGE cuts hit agencies that regulate or compete with Musk's companies"},
			{Source: "New York Times", Author: "Maggie Haberman, Eric Lipton", Date: "2025-01-29",
				Summary: "Musk's government role raises conflict-of-interest questions on SpaceX contracts"},
			{Source: "Washington Post", Author: "Cat Zakrzewski, Faiz Siddiqui", Date: "2025-03-01",
				Summary: "FAA staffing cuts by DOGE raise safety concerns; SpaceX awaits launch approvals"},
		},
		StopMessage: "You cannot stop doge-conflict-of-interest.\nThe Office of Government Ethics has questions.\n" +
			"The questions are still pending. DOGE is still running.\nWe note the irony.",
	},
	{
		ID:     "starship-faa-delays",
		Name:   "starship-faa-delays",
		Since:  "2023-01-01",
		Status: "RUNNING",
		Description: "SpaceX's Starship program has faced repeated FAA launch license delays due to " +
			"environmental reviews, programmatic safety reviews, and the aftermath of IFT-1's " +
			"launch pad destruction.",
		References: []DaemonRef{
			{Source: "Reuters", Author: "Joey Roulette", Date: "2023-04-18",
				Summary: "FAA requires 63 corrective actions before next Starship flight after IFT-1"},
			{Source: "Ars Technica", Author: "Eric Berger", Date: "2023-06-13",
				Summary: "Deep dive: why Starship's FAA approval process takes months, not weeks"},
			{Source: "The Guardian", Author: "Dharna Noor", Date: "2023-04-21",
				Summary: "Environmental groups cite concrete debris cloud from IFT-1 as regulatory failure"},
		},
		StopMessage: "You cannot stop starship-faa-delays.\nThe FAA has 63 corrective actions per flight.\n" +
			"Multiplied by 6 flights = 378 actions. Some are complete.\nFiling continues.",
	},
	{
		ID:     "tesla-autopilot-investigations",
		Name:   "tesla-autopilot-investigations",
		Since:  "2021-08-01",
		Status: "RUNNING",
		Description: "NHTSA opened multiple investigations into Tesla Autopilot/FSD following fatal crashes " +
			"and incidents involving emergency vehicles. Largest automotive safety probe in NHTSA history.",
		References: []DaemonRef{
			{Source: "NHTSA", Author: "National Highway Traffic Safety Administration", Date: "2021-08-16",
				Summary: "NHTSA opens formal investigation into Tesla Autopilot; 11 emergency vehicle crashes"},
			{Source: "Washington Post", Author: "Faiz Siddiqui", Date: "2023-02-16",
				Summary: "NHTSA upgrades investigation; 362,000 vehicles subject to recall for FSD"},
			{Source: "New York Times", Author: "Jack Ewing, Neal Boudette", Date: "2024-04-27",
				Summary: "Tesla settled wrongful-death suits; Autopilot safety data remains disputed"},
		},
		StopMessage: "You cannot stop tesla-autopilot-investigations.\nNHTSA is still collecting data.\n" +
			"The cars are still driving. Some are still crashing.\nFull Self-Driving is a subscription.",
	},
	{
		ID:     "spacex-settlement-nlrb",
		Name:   "spacex-settlement-nlrb",
		Since:  "2023-06-15",
		Status: "RUNNING",
		Description: "NLRB filed complaint that SpaceX illegally fired eight employees who wrote an open " +
			"letter criticizing Elon Musk's Twitter behavior. SpaceX filed to abolish the NLRB.",
		References: []DaemonRef{
			{Source: "NLRB Filing", Author: "National Labor Relations Board", Date: "2023-06-15",
				Summary: "NLRB: SpaceX unlawfully terminated workers who circulated open letter criticizing Musk"},
			{Source: "Bloomberg", Author: "Josh Eidelson", Date: "2023-11-06",
				Summary: "SpaceX lawsuit challenges NLRB constitutionality; novel legal strategy"},
			{Source: "Reuters", Author: "Daniel Wiessner", Date: "2024-03-08",
				Summary: "SpaceX argues NLRB judges unconstitutionally appointed; case ongoing"},
		},
		StopMessage: "You cannot stop spacex-settlement-nlrb.\nSpaceX's response to the labor board was to sue the labor board.\n" +
			"This is not the most efficient path. It is very on-brand.",
	},
	{
		ID:     "neuralink-animal-welfare",
		Name:   "neuralink-animal-welfare",
		Since:  "2022-12-05",
		Status: "RUNNING",
		Description: "Reuters investigation reported ~1,500 animal deaths during Neuralink's testing phase, " +
			"with employees citing rushed timelines. USDA launched an investigation.",
		References: []DaemonRef{
			{Source: "Reuters Investigative", Author: "Rachael Levy, Brian Grow", Date: "2022-12-05",
				Summary: "Exclusive: Neuralink faces federal probe; rushed animal tests led to ~1,500 deaths"},
			{Source: "Washington Post", Author: "Pranshu Verma", Date: "2023-02-28",
				Summary: "USDA opens formal inquiry into Neuralink animal testing procedures"},
			{Source: "Reuters", Author: "Rachael Levy", Date: "2023-08-14",
				Summary: "Neuralink receives FDA approval for human trials despite ongoing probe"},
		},
		StopMessage: "You cannot stop neuralink-animal-welfare.\nThe FDA approved human trials anyway.\n" +
			"The first human patient received an implant in January 2024.\nWe are not in a simulation.",
	},
	{
		ID:     "sec-vs-elon-twitter-poll",
		Name:   "sec-vs-elon-twitter-poll",
		Since:  "2022-11-07",
		Status: "RUNNING",
		Description: "Musk ran a Twitter poll asking if he should sell Tesla shares, then sold $3.9B worth. " +
			"SEC subpoenaed Musk; dispute over whether he properly disclosed Twitter stake acquisition.",
		References: []DaemonRef{
			{Source: "SEC Complaint", Author: "U.S. Securities and Exchange Commission", Date: "2023-01-17",
				Summary: "SEC sues Musk for late disclosure of Twitter stake acquisition (10-day delay, $150M benefit)"},
			{Source: "Bloomberg", Author: "Matt Robinson, Erik Larson", Date: "2023-02-15",
				Summary: "Musk defiant in SEC deposition; claims disclosure requirements unclear"},
			{Source: "New York Times", Author: "David Gelles, Lauren Hirsch", Date: "2022-11-08",
				Summary: "The $44B Twitter deal, the Twitter poll, and what the SEC wants to know"},
		},
		StopMessage: "You cannot stop sec-vs-elon-twitter-poll.\nThe SEC filed in 2023.\nMusk's lawyers filed responses.\n" +
			"The disclosure was 10 days late. $150M was at stake.\nCourt dates continue to be scheduled.",
	},
}

// FindCompetitor returns competitor matching id (case-insensitive slug or name).
func FindCompetitor(id string) (*Competitor, bool) {
	id = strings.ToLower(strings.ReplaceAll(id, " ", "-"))
	for i := range Competitors {
		c := &Competitors[i]
		if strings.ToLower(c.ID) == id ||
			strings.ToLower(strings.ReplaceAll(c.Name, " ", "-")) == id {
			return c, true
		}
	}
	return nil, false
}

// FindDaemon returns daemon matching id (case-insensitive slug or name).
func FindDaemon(id string) (*Daemon, bool) {
	id = strings.ToLower(strings.ReplaceAll(id, " ", "-"))
	for i := range Daemons {
		d := &Daemons[i]
		if strings.ToLower(d.ID) == id ||
			strings.ToLower(strings.ReplaceAll(d.Name, " ", "-")) == id {
			return d, true
		}
	}
	return nil, false
}
