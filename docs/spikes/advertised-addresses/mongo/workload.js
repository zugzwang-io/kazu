// Seeded with mongo-1 only; writes go to the discovered primary and w: 3 needs all three members.
const w = connect("mongodb://mongo-1:27017/shop?replicaSet=rs0&serverSelectionTimeoutMS=15000");
print("primary=" + w.hello().primary);
for (let i = 0; i < 100; i++) w.orders.insertOne({ i }, { writeConcern: { w: 3 } });
print("count=" + w.orders.countDocuments());
