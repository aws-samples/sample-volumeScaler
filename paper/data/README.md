# Evaluation Data and Analysis

This directory contains the datasets and analysis code used to produce the figures in the paper.

## Datasets

The data was collected during a 30-day evaluation of VolumeScaler deployed on Amazon EKS with EBS-backed PersistentVolumeClaims using the `gp2` StorageClass. Two scaling approaches were compared side-by-side:

- `automated_dataset.csv` — Metrics from the automated (VolumeScaler) scaling scenario
- `manual_dataset.csv` — Metrics from the manual operator-driven scaling scenario
- `combined_dataset.csv` — Both scenarios combined for comparative analysis

### Columns

| Column | Description |
|---|---|
| Day | Day of the 30-day evaluation period |
| Actual_Volume_GB | Actual storage usage (GiB) |
| Provisioned_Volume_GB | Storage provisioned by the scaling approach |
| Daily_Cost | Daily cost in USD (AWS EBS gp2 at $0.08/GB-month) |
| Manual_Interventions | Number of manual operator interventions required |
| MTBF | Mean Time Between Failures (hours) |
| MTTR | Mean Time To Recovery (hours) |
| Failures | Whether a failure occurred on that day (0 or 1) |
| Scaling | Scaling approach ("Manual" or "Automated") |

## Reproducing the Plots

The `analysis_code.py` script reads the CSV datasets and produces all figures referenced in the paper.

```bash
pip install pandas seaborn matplotlib
python analysis_code.py
```

Plots are saved to a `plots/` subdirectory.
