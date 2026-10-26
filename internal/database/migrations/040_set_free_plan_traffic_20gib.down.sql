-- Restore the previous free plan quota: 20 GiB -> 50 GiB (53687091200 bytes).
UPDATE plans SET traffic_limit = 53687091200 WHERE name = 'free';
