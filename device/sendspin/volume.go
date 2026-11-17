package sendspin

func PercentToLevel(pct, max int) int { return (clamp(pct, 0, 100)*max + 50) / 100 }

func LevelToPercent(level, max int) int { return (clamp(level, 0, max)*100 + max/2) / max }
