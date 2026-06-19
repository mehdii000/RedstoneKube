package mc.parkour;

/** Pure helper, unit-testable without Bukkit (shared contract with the stub game). */
public final class Done {
  private Done() {}

  /** POST target on the controller; it then unregisters + deletes this pod. */
  public static String doneUrl(String base, String instanceId) {
    if (base.endsWith("/")) base = base.substring(0, base.length() - 1);
    return base + "/instances/" + instanceId + "/done";
  }
}
