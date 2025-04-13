package main

import (
	"testing"
	"time"
)

func TestStorageSize_ToGiB(t *testing.T) {
	tests := []struct {
		name     string
		size     StorageSize
		expected float64
		wantErr  bool
	}{
		{
			name:     "valid GiB",
			size:     "5Gi",
			expected: 5.0,
			wantErr:  false,
		},
		{
			name:     "valid MiB",
			size:     "1024Mi",
			expected: 1.0,
			wantErr:  false,
		},
		{
			name:     "valid TiB",
			size:     "1Ti",
			expected: 1024.0,
			wantErr:  false,
		},
		{
			name:     "invalid format",
			size:     "invalid",
			expected: 0.0,
			wantErr:  true,
		},
		{
			name:     "empty string",
			size:     "",
			expected: 0.0,
			wantErr:  true,
		},
		{
			name:     "number only",
			size:     "5",
			expected: 5.0,
			wantErr:  false,
		},
		{
			name:     "invalid unit",
			size:     "5Xi",
			expected: 0.0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.size.ToGiB()
			if (err != nil) != tt.wantErr {
				t.Errorf("StorageSize.ToGiB() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("StorageSize.ToGiB() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPercentage_ToFloat(t *testing.T) {
	tests := []struct {
		name     string
		percent  Percentage
		expected float64
		wantErr  bool
	}{
		{
			name:     "valid percentage",
			percent:  "70%",
			expected: 70.0,
			wantErr:  false,
		},
		{
			name:     "decimal percentage",
			percent:  "70.5%",
			expected: 70.5,
			wantErr:  false,
		},
		{
			name:     "zero percentage",
			percent:  "0%",
			expected: 0.0,
			wantErr:  false,
		},
		{
			name:     "no percentage sign",
			percent:  "70",
			expected: 70.0,
			wantErr:  false,
		},
		{
			name:     "invalid format",
			percent:  "invalid%",
			expected: 0.0,
			wantErr:  true,
		},
		{
			name:     "empty string",
			percent:  "",
			expected: 0.0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.percent.ToFloat()
			if (err != nil) != tt.wantErr {
				t.Errorf("Percentage.ToFloat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("Percentage.ToFloat() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConvertToGi(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		wantErr  bool
	}{
		{
			name:     "valid GiB",
			input:    "5Gi",
			expected: 5.0,
			wantErr:  false,
		},
		{
			name:     "valid MiB",
			input:    "1024Mi",
			expected: 1.0,
			wantErr:  false,
		},
		{
			name:     "valid TiB",
			input:    "1Ti",
			expected: 1024.0,
			wantErr:  false,
		},
		{
			name:     "invalid format",
			input:    "invalid",
			expected: 0.0,
			wantErr:  true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0.0,
			wantErr:  true,
		},
		{
			name:     "number only",
			input:    "5",
			expected: 5.0,
			wantErr:  false,
		},
		{
			name:     "invalid unit",
			input:    "5Xi",
			expected: 0.0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertToGi(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertToGi() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("convertToGi() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseCooldownDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{
			name:     "valid duration",
			input:    "10m",
			expected: 10 * time.Minute,
			wantErr:  false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "invalid duration",
			input:    "invalid",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "hours duration",
			input:    "2h",
			expected: 2 * time.Hour,
			wantErr:  false,
		},
		{
			name:     "complex duration",
			input:    "1h30m",
			expected: 90 * time.Minute,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCooldownDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCooldownDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("parseCooldownDuration() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCanScaleNow(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name         string
		lastScaledAt string
		cooldown     time.Duration
		expected     bool
		wantErr      bool
	}{
		{
			name:         "no cooldown",
			lastScaledAt: "",
			cooldown:     0,
			expected:     true,
			wantErr:      false,
		},
		{
			name:         "in cooldown",
			lastScaledAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			cooldown:     10 * time.Minute,
			expected:     false,
			wantErr:      false,
		},
		{
			name:         "cooldown expired",
			lastScaledAt: now.Add(-15 * time.Minute).Format(time.RFC3339),
			cooldown:     10 * time.Minute,
			expected:     true,
			wantErr:      false,
		},
		{
			name:         "invalid timestamp",
			lastScaledAt: "invalid",
			cooldown:     10 * time.Minute,
			expected:     false,
			wantErr:      true,
		},
		{
			name:         "zero cooldown with timestamp",
			lastScaledAt: now.Format(time.RFC3339),
			cooldown:     0,
			expected:     true,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canScaleNow(tt.lastScaledAt, tt.cooldown)
			if (err != nil) != tt.wantErr {
				t.Errorf("canScaleNow() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("canScaleNow() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestComputeNewSize(t *testing.T) {
	tests := []struct {
		name          string
		scale         string
		scaleType     string
		currentSizeGi float64
		expected      float64
		wantErr       bool
	}{
		{
			name:          "fixed increase",
			scale:         "2Gi",
			scaleType:     "fixed",
			currentSizeGi: 5.0,
			expected:      7.0,
			wantErr:       false,
		},
		{
			name:          "percentage increase",
			scale:         "20%",
			scaleType:     "percentage",
			currentSizeGi: 10.0,
			expected:      12.0,
			wantErr:       false,
		},
		{
			name:          "VolumeScaler type",
			scale:         "30%",
			scaleType:     "VolumeScaler",
			currentSizeGi: 10.0,
			expected:      13.0,
			wantErr:       false,
		},
		{
			name:          "invalid fixed scale",
			scale:         "invalid",
			scaleType:     "fixed",
			currentSizeGi: 5.0,
			expected:      0.0,
			wantErr:       true,
		},
		{
			name:          "invalid percentage",
			scale:         "invalid%",
			scaleType:     "percentage",
			currentSizeGi: 5.0,
			expected:      0.0,
			wantErr:       true,
		},
		{
			name:          "unknown scale type with valid percentage",
			scale:         "20%",
			scaleType:     "unknown",
			currentSizeGi: 10.0,
			expected:      12.0,
			wantErr:       false,
		},
		{
			name:          "unknown scale type with invalid percentage",
			scale:         "invalid%",
			scaleType:     "unknown",
			currentSizeGi: 10.0,
			expected:      0.0,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computeNewSize(tt.scale, tt.scaleType, tt.currentSizeGi)
			if (err != nil) != tt.wantErr {
				t.Errorf("computeNewSize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("computeNewSize() = %v, want %v", got, tt.expected)
			}
		})
	}
}
