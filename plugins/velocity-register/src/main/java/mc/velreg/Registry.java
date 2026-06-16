package mc.velreg;

/** Minimal backend registry, so the handler is testable without a live proxy. */
public interface Registry {
  void register(String name, String address);   // idempotent upsert
  boolean unregister(String name);               // false if name was unknown
}
