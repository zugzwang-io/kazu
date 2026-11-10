// Seeded with mongo-1 only. The client must discover mongo-3 as primary to write, then dial every member
// the set reports, so each one has to be reachable at the address it is registered under.
const fail = (msg) => { print("FAIL " + msg); quit(1); };
const w = connect("mongodb://mongo-1:27017/shop?replicaSet=rs0&serverSelectionTimeoutMS=15000");
const hello = w.hello();
print("primary=" + hello.primary);
if (hello.primary !== "mongo-3:27017") fail("primary is not mongo-3");
for (let i = 0; i < 100; i++) w.orders.insertOne({ i }, { writeConcern: { w: 3, wtimeout: 10000 } });
for (const h of hello.hosts) {
  const me = connect(`mongodb://${h}/?directConnection=true&serverSelectionTimeoutMS=10000`).hello().me;
  print("dialed " + h + " -> " + me);
  if (me !== h) fail(h + " answered as " + me);
}
const n = w.orders.countDocuments();
print("count=" + n);
if (hello.hosts.length !== 3 || n !== 100) fail("expected 3 members and 100 documents");
