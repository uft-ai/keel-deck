-- Whether the provider reported token counters.
-- NULL is a legacy record that omitted the field. 0 means the provider
-- explicitly reported no usable counters. 1 means the counters were reported,
-- including genuine zeros. Existing integer counters are unchanged.
ALTER TABLE usage_events ADD COLUMN tokens_reported INTEGER;
