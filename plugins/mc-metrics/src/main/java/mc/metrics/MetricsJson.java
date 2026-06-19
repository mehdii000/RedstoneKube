package mc.metrics;

import java.util.Locale;

/** Pure JSON builder — no Bukkit, so it unit-tests without paper-api. */
public final class MetricsJson {
  private MetricsJson() {}

  public static String build(double tps, double mspt, int players, int maxPlayers,
                             long uptimeSec, double jvmStartupSec) {
    return String.format(Locale.ROOT,
      "{\"tps\":%.2f,\"mspt\":%.2f,\"players\":%d,\"maxPlayers\":%d,\"uptimeSec\":%d,\"jvmStartupSec\":%.1f}",
      tps, mspt, players, maxPlayers, uptimeSec, jvmStartupSec);
  }
}
