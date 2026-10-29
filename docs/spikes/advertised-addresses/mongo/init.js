// mongo-3 gets the highest priority, so a client seeded with mongo-1 must discover it to write.
const host = (i) => (process.env.MODE === "ips" ? `10.77.0.1${i}` : `mongo-${i}`) + ":27017";
rs.initiate({ _id: "rs0", members: [1, 2, 3].map((i) => ({ _id: i, host: host(i), priority: i === 3 ? 2 : 1 })) });
while ((db.hello().primary || "") !== host(3)) sleep(500);
