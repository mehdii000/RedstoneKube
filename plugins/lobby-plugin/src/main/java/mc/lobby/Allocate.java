package mc.lobby;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Pure helper for parsing the controller's /allocate response. */
public final class Allocate {
  private Allocate() {}
  private static final Pattern SERVER = Pattern.compile("\"server\"\\s*:\\s*\"([^\"]+)\"");

  // ponytail: regex extract of one field; swap for a JSON lib if the response grows.
  public static String parseServer(String json) {
    if (json == null) return null;
    Matcher m = SERVER.matcher(json);
    return m.find() ? m.group(1) : null;
  }
}
