package mc.velreg;

import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Pure request logic: no HttpExchange, no proxy — testable with a fake Registry. */
public final class ServersHandler {
  private ServersHandler() {}

  private static final Pattern NAME = Pattern.compile("\"name\"\\s*:\\s*\"([^\"]+)\"");
  private static final Pattern ADDR = Pattern.compile("\"address\"\\s*:\\s*\"([^\"]+)\"");

  /** Returns the HTTP status code; mutates the registry on success. */
  public static int handle(String method, String path, String auth, String body, String token, Registry reg) {
    if (token != null && !token.isEmpty() && !("Bearer " + token).equals(auth)) return 401;
    switch (method) {
      case "POST": {
        if (!path.equals("/servers")) return 404;
        String name = group(NAME, body), addr = group(ADDR, body);
        if (name == null || addr == null) return 400;
        reg.register(name, addr);
        return 200;
      }
      case "DELETE": {
        String prefix = "/servers/";
        if (!path.startsWith(prefix) || path.length() <= prefix.length()) return 400;
        String name = path.substring(prefix.length());
        return reg.unregister(name) ? 200 : 404;
      }
      default:
        return 405;
    }
  }

  // ponytail: regex field extract for two known keys; swap for Gson if the payload grows.
  private static String group(Pattern p, String s) {
    if (s == null) return null;
    Matcher m = p.matcher(s);
    return m.find() ? m.group(1) : null;
  }
}
