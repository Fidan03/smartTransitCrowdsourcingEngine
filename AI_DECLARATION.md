AI USAGE DECLARATION
====================

Project:     Smart Transit & Crowdsourcing Engine (smartTransitCrowdsourcingEngine)
Repository:  https://github.com/Fidan03/smartTransitCrowdsourcingEngine
Version:     1.0
Date:        31 October 2026
Team:        [Team member names]
Course:      [Course / programme name]
Supervisor:  [Supervisor name]


1. DECLARATION
--------------

We, the project team, declare that generative artificial intelligence (AI) was
used substantially in the development of this project, as described below. We
make this declaration to be transparent about how the work was produced,
including the fact that the Git commit history is authored under the team's
own account and does not itself show AI involvement.

The team directed the work, made the design decisions, reviewed the results,
and takes full responsibility for the submitted project, including any errors
it may contain.


2. AI TOOL USED
---------------

  Tool:           Claude Code (command-line agentic coding assistant)
  Provider:       Anthropic
  Model:          Claude Opus 5.5
  Period of use:  31 October 2026
  Environment:    Run locally on the team's Windows 10 computer. The assistant
                  could read and write files in the project folder, run
                  commands (Go, PostgreSQL, Git), search the web, and download
                  public open data.

No other AI tools were used for this project, unless listed here by the team:
  [none / list other tools]


3. WHAT THE TEAM DID (HUMAN CONTRIBUTIONS)
------------------------------------------

  - Wrote the project plan (plan.md): the problem, the functional requirements,
    the architecture direction and the acceptance criteria.
  - Made the scope decisions (plan section 20): simulated GPS, per-route
    crowding, plain HTML/JS frontend, PostgreSQL, simple operator login.
  - Directed each phase of the work and decided when to continue, change
    direction or add features (for example, integrating real opendata.az data
    and the "expected crowding" estimate shown when there are no reports).
  - Tested the user interface by hand in the browser and reported the problems
    found: map tiles blocked, login view not hiding, the data-source section's
    position, and requests for a modern, natural design.
  - Owns the repository and controls what is published.
  - Is responsible for confirming third-party data licences (see section 8).


4. WHAT THE AI DID (AI CONTRIBUTIONS)
-------------------------------------

Under the team's direction, the AI assistant:

  - set up the local environment (installed Go and PostgreSQL; created the
    database, the app user and the test database; generated local secrets);
  - designed and wrote the Go backend, the SQL schema and migrations, the GPS
    simulator, the crowding rule, the alert engine, the ETA estimation,
    operator authentication and the REST API;
  - researched opendata.az, found the public CKAN API behind the site,
    evaluated the datasets and wrote the importer that matches stops to routes;
  - wrote the passenger and operator web pages and the visual design;
  - wrote the automated tests and the demo script;
  - debugged and fixed the problems found during testing;
  - drafted the documentation: README, this declaration and the technical
    documentation.

  Area                          Primarily by    Reviewed / directed by
  ----------------------------  --------------  ----------------------
  Requirements and plan         Team            Team
  Scope and design decisions    Team            Team
  Source code (backend, UI)     AI              Team
  Database schema               AI              Team
  Automated tests               AI              Team
  Open data research/import     AI              Team
  Manual UI testing             Team            Team
  Documentation drafts          AI              Team


5. ARCHITECTURE USED
--------------------

The system is a MODULAR MONOLITH: one Go program with clearly separated
internal packages, one PostgreSQL database, and a web frontend served by the
same program. Microservices were deliberately avoided, as the plan
recommends.

  Passenger web page        Operator dashboard
           \                       /
            +-----> Go backend <--+        (REST API, polling every 3-5 s)
                       |
     +-----------------+------------------+
     |        |        |        |         |
  Transit   GPS +    Crowd    Alert     Demand
  data      ETA      rule     engine    estimate
     |        |        |        |         |
     +-----------------+------------------+
                       |
                  PostgreSQL
                       |
        GPS simulator (Provider interface)     opendata.az importer

Key architectural choices:
  - The GPS source sits behind a Provider interface, so a real feed (such as
    GTFS-Realtime) can later replace the simulator without changing the
    crowding or alert logic.
  - Crowding and alert rules run only on the backend, so passengers and
    operators always see the same result.
  - Alert state (the sustained-HIGH timer) is stored in PostgreSQL, not in
    memory, so it survives restarts.
  - The expected-crowding estimate is display-only and is never used by the
    alert engine.
  - Time is injected through a clock interface, so time-based rules can be
    tested without waiting.


6. HOW THE PROJECT WAS TESTED
-----------------------------

  Automated tests: 119 passing, 0 failing (Go "testing" package).

  a) Unit tests (no database), for example:
     - the crowd rule: the plan's example table, the 60% threshold boundaries,
       the 10-minute window, one report per passenger;
     - the alert timer: exactly at 10 minutes and not before, reset on
       recovery, UNKNOWN handling, outage gaps;
     - ETA maths and the "unavailable" cases (stale GPS, off route);
     - simulator movement, dwelling and trip turnaround;
     - open-data parsing, path stitching, direction split, right-hand-side
       stop matching;
     - expected-crowding levels, Baku time zone and weekend patterns;
     - HTTP handlers: validation, authentication, roles, rate limiting.

  b) Integration tests against a real PostgreSQL test database
     (smart_transit_test, wiped on each run), for example:
     - one alert at exactly 10 minutes, with no duplicates even under 8
       concurrent evaluations;
     - the timer surviving a simulated backend restart;
     - the alert lifecycle (acknowledge, resolve) and the audit log;
     - stale GPS withholding ETAs;
     - login, session expiry and logout;
     - the open-data import replacing demo data correctly.

  c) Build verification: every commit was checked out separately and compiled
     with "go vet" to confirm it builds on its own.

  d) End-to-end checks on the running server: crowd reports, duplicate
     rejection, alert creation, acknowledge/resolve, login/logout, the
     production safety lock, and the real-data endpoints.

  e) Manual browser testing by the team. Automated screenshots were not
     possible on the lab computer (headless browsers are blocked), so visual
     verification of the UI relies on the team.

Testing limits: passing tests show that the implemented rules behave as
specified; they do not prove that the thresholds, the ETA baseline or the
expected-crowding estimate are accurate for real Baku conditions. These need
validation with real operational data (plan sections 6 and 7).


7. VERIFICATION AND ACCOUNTABILITY
----------------------------------

  - AI output can contain mistakes. The team reviewed the results, ran the
    application, and tested it by hand.
  - Several defects were found and fixed during development, both by the
    automated tests and by the team's manual testing. The technical
    documentation lists them.
  - Known limitations are documented in README.md and in the technical
    documentation.


8. DATA, PRIVACY AND THIRD-PARTY CONTENT
----------------------------------------

  - No personal data of real passengers was collected or given to the AI.
    Passenger reports are anonymous; submitter keys are HMAC hashes.
  - Local secrets (database passwords, operator password, HMAC key) live only
    in the .env file, which is excluded from Git.
  - Real data comes from opendata.az:
      * AYNA bus route paths: CC0
      * BakuBus daily ridership: CC BY (credited in the application)
      * BakuBus stop coordinates: LICENCE NOT SPECIFIED by the publisher.
        The team must confirm permission before any public deployment.
  - Bus positions are simulated, and this is labelled in the application.
  - Third-party code: Leaflet 1.9.4 (BSD-2-Clause, bundled), pgx (MIT),
    golang.org/x/crypto (BSD-3-Clause). Map tiles: (c) OpenStreetMap
    contributors.


9. CONFIRMATION
---------------

By signing below, each team member confirms that this declaration accurately
describes how AI was used in this project.

  Name: ______________________   Signature: ______________   Date: __________

  Name: ______________________   Signature: ______________   Date: __________

  Name: ______________________   Signature: ______________   Date: __________
