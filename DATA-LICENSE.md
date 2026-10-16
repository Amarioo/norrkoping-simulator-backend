# Map data attribution and licence

The derived database `src/lib/data/norrkoping-map.json` and `src/lib/data/transport-network.json` (also distributed as the backend resource `transport-network.json`) contain data from **© OpenStreetMap contributors**, obtained through the public Overpass API.

- Source and attribution: https://www.openstreetmap.org/copyright
- Database licence: Open Data Commons Open Database Licence (ODbL) 1.0, https://opendatacommons.org/licenses/odbl/1-0/
- Snapshot time, geographic bounds, origin, scale and extraction query are recorded in the file's `meta` object.
- The derived database is made available under ODbL 1.0. Retain attribution and follow its redistribution/share-alike requirements when sharing this data or further adapted databases.

The importer converts coordinates to a local 1:25 scene, clips polygons, assigns land-cover classes and estimates missing building heights/materials. Architectural details and supplemental planting are illustrative additions. The reference screenshot supplied by the user is not used as a map texture or redistributed asset.

This notice covers the map database; it does not change the licence of application code or dependencies.

