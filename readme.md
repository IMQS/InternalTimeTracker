Scraper to integrate stats from JIRA and TMetric

## Getting Started
1. Setup a `config` directory with the appropriate 3 config files inside. See sample-config for examples.
	For the TMetric authorization information, just login as a user, and then steal the cookies from that session. TMetric
	doesn't have a nice login API, so we just hack it like this.
2. Create a Postgres database for storing the data
3. Run `env`
4. 