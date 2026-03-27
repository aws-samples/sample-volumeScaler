import pandas as pd
import seaborn as sns
import matplotlib.pyplot as plt
import os

# Create directory for plots if it doesn't exist
os.makedirs('plots', exist_ok=True)

# ------------------------------------------------------------------------------
# 1) Load Datasets
# ------------------------------------------------------------------------------
manual_df = pd.read_csv('manual_dataset.csv')
auto_df = pd.read_csv('automated_dataset.csv')
combined_df = pd.read_csv('combined_dataset.csv')

# ------------------------------------------------------------------------------
# 2) Visualization
# ------------------------------------------------------------------------------
sns.set(style="whitegrid", palette="pastel")

# Plot A: Daily Cost Comparison
plt.figure(figsize=(12, 6))
sns.lineplot(data=combined_df, x="Day", y="Daily_Cost", hue="Scaling",
             style="Scaling", markers=True, dashes=False)
plt.title("Daily Storage Cost Comparison\n(Manual vs Automated Scaling)")
plt.xlabel("Day of Month")
plt.ylabel("Daily Cost ($)")
plt.legend(title="Scaling Strategy")
plt.tight_layout()
plt.savefig('plots/daily_cost_comparison.png', dpi=300, bbox_inches='tight')
plt.close()

# Plot B: Provisioned vs Actual Usage
plt.figure(figsize=(12, 6))
sns.lineplot(data=combined_df, x="Day", y="Provisioned_Volume_GB", hue="Scaling")
sns.lineplot(data=combined_df, x="Day", y="Actual_Volume_GB",
             color="black", linestyle="--", label="Actual Usage")
plt.title("Storage Provisioning vs Actual Usage")
plt.xlabel("Day of Month")
plt.ylabel("Storage Volume (GiB)")
plt.legend(title="Legend")
plt.tight_layout()
plt.savefig('plots/provisioned_vs_actual.png', dpi=300, bbox_inches='tight')
plt.close()

# Plot C: Reliability Metrics
fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(12, 10))

# MTBF Plot
sns.lineplot(data=combined_df, x="Day", y="MTBF", hue="Scaling", ax=ax1)
ax1.set_title("Mean Time Between Failures (MTBF)")
ax1.set_ylabel("Hours")

# MTTR Plot
sns.lineplot(data=combined_df, x="Day", y="MTTR", hue="Scaling", ax=ax2)
ax2.set_title("Mean Time to Recovery (MTTR)")
ax2.set_ylabel("Hours")

plt.tight_layout()
plt.savefig('plots/reliability_metrics.png', dpi=300, bbox_inches='tight')
plt.close()

# Plot D: Failure Events
plt.figure(figsize=(12, 4))
sns.scatterplot(data=combined_df, x="Day", y="Failures", hue="Scaling",
                style="Scaling", s=100)
plt.title("Storage Failure Events Distribution")
plt.yticks([0, 1])
plt.xlabel("Day of Month")
plt.ylabel("Failure Occurrence")
plt.tight_layout()
plt.savefig('plots/failure_events.png', dpi=300, bbox_inches='tight')
plt.close()

# Plot E: Cumulative Cost Analysis
plt.figure(figsize=(12, 6))
combined_df['Cumulative_Cost'] = combined_df.groupby('Scaling')['Daily_Cost'].cumsum()
sns.lineplot(data=combined_df, x="Day", y="Cumulative_Cost", hue="Scaling")
plt.title("Cumulative Storage Costs Over Time")
plt.xlabel("Day of Month")
plt.ylabel("Cumulative Cost ($)")
plt.tight_layout()
plt.savefig('plots/cumulative_cost.png', dpi=300, bbox_inches='tight')
plt.close()

# Plot F: Cost Difference Analysis
cost_comparison = pd.pivot_table(combined_df, values='Daily_Cost',
                                index='Day', columns='Scaling').reset_index()
cost_comparison['Cost_Savings'] = cost_comparison['Manual'] - cost_comparison['Automated']

plt.figure(figsize=(12, 6))
sns.barplot(data=cost_comparison, x="Day", y="Cost_Savings", color="skyblue")
plt.axhline(0, color="gray", linestyle="--")
plt.title("Daily Cost Savings from Automated Scaling")
plt.xlabel("Day of Month")
plt.ylabel("Savings vs Manual ($)")
plt.tight_layout()
plt.savefig('plots/cost_savings.png', dpi=300, bbox_inches='tight')
plt.close()

# ------------------------------------------------------------------------------
# 3) Statistical Summary
# ------------------------------------------------------------------------------
manual_total = combined_df[combined_df['Scaling'] == 'Manual']['Daily_Cost'].sum()
automated_total = combined_df[combined_df['Scaling'] == 'Automated']['Daily_Cost'].sum()
savings = manual_total - automated_total
saving_percentage = (savings / manual_total * 100) if manual_total != 0 else 0

summary = (
    f"Manual Total Cost: ${manual_total:.2f}\n" +
    f"Automated Total Cost: ${automated_total:.2f}\n" +
    f"30-day Savings: ${savings:.2f} ({saving_percentage:.2f}%)\n"
)

with open('plots/cost_summary.txt', 'w') as f:
    f.write(summary)

print(summary)
