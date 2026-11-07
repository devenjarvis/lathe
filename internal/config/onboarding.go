package config

// Onboarded reports whether the reader has finished or skipped the
// first-launch setup. An unreadable config.json reads as done, so a broken
// file never traps the reader in the setup flow.
func Onboarded() bool {
	c, err := ReadConfig()
	if err != nil {
		return true
	}
	return c.Onboarded
}

// SetOnboarded records whether the first-launch setup is done.
func SetOnboarded(done bool) error {
	return UpdateConfig(func(c *Config) error {
		c.Onboarded = done
		return nil
	})
}
