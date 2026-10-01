const LEAFLET = 'https://unpkg.com/leaflet@1.9.4/dist/leaflet';

function load(tag, attrs) {
  return new Promise((resolve, reject) => {
    const el = document.createElement(tag);
    Object.assign(el, attrs);
    el.onload = resolve;
    el.onerror = reject;
    document.head.appendChild(el);
  });
}

const darkStyle = [
  { elementType: 'geometry', stylers: [{ color: '#1b1f27' }] },
  { elementType: 'labels.text.fill', stylers: [{ color: '#98a2b3' }] },
  { elementType: 'labels.text.stroke', stylers: [{ color: '#13161b' }] },
  { featureType: 'water', elementType: 'geometry', stylers: [{ color: '#0b0d10' }] },
  { featureType: 'road', stylers: [{ visibility: 'off' }] },
];

class GoogleView {
  async init(el, key, onClick) {
    await new Promise((resolve, reject) => {
      window.__mapsReady = resolve;
      load('script', { src: `https://maps.googleapis.com/maps/api/js?key=${encodeURIComponent(key)}&callback=__mapsReady&loading=async`, async: true }).catch(reject);
    });
    const dark = matchMedia('(prefers-color-scheme: dark)').matches;
    this.map = new google.maps.Map(el, { center: { lat: 20, lng: 10 }, zoom: 2, minZoom: 2, streetViewControl: false, mapTypeControl: false, styles: dark ? darkStyle : [] });
    this.map.addListener('click', (e) => onClick(e.latLng.lat(), e.latLng.lng()));
    this.pins = []; this.lines = []; this.dest = null;
  }
  setPeople(people, onDrag) {
    this.pins.forEach((m) => m.setMap(null));
    this.pins = people.map((p, i) => {
      const m = new google.maps.Marker({ position: { lat: p.lat, lng: p.lng }, map: this.map, draggable: true, title: p.name, label: { text: String(i + 1), color: '#06101f', fontSize: '11px', fontWeight: '700' } });
      m.addListener('dragend', (e) => onDrag(i, e.latLng.lat(), e.latLng.lng()));
      return m;
    });
  }
  showOption(origins, dest) {
    this.lines.forEach((l) => l.setMap(null));
    if (this.dest) this.dest.setMap(null);
    this.lines = []; this.dest = null;
    if (!dest) return;
    const bounds = new google.maps.LatLngBounds();
    this.dest = new google.maps.Marker({ position: dest, map: this.map, title: dest.label, icon: { path: google.maps.SymbolPath.CIRCLE, scale: 9, fillColor: '#3ecf8e', fillOpacity: 1, strokeColor: '#fff', strokeWeight: 2 } });
    bounds.extend(dest);
    for (const o of origins) {
      this.lines.push(new google.maps.Polyline({ path: [o, dest], geodesic: true, strokeColor: '#4f8cff', strokeOpacity: .7, strokeWeight: 2, map: this.map }));
      bounds.extend(o);
    }
    this.map.fitBounds(bounds, 60);
  }
  fit(people) {
    if (!people.length) return;
    const b = new google.maps.LatLngBounds();
    people.forEach((p) => b.extend({ lat: p.lat, lng: p.lng }));
    this.map.fitBounds(b, 80);
    if (people.length === 1) this.map.setZoom(5);
  }
}

class LeafletView {
  async init(el, _key, onClick) {
    await load('link', { rel: 'stylesheet', href: LEAFLET + '.css' });
    await load('script', { src: LEAFLET + '.js' });
    this.map = L.map(el, { worldCopyJump: true }).setView([20, 10], 2);
    this.map.setMinZoom(Math.max(1, this.map.getBoundsZoom([[-60, -180], [75, 180]], false)));
    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 18, attribution: '&copy; OpenStreetMap' }).addTo(this.map);
    this.map.on('click', (e) => onClick(e.latlng.lat, e.latlng.lng));
    this.pins = L.layerGroup().addTo(this.map);
    this.overlay = L.layerGroup().addTo(this.map);
  }
  setPeople(people, onDrag) {
    this.pins.clearLayers();
    people.forEach((p, i) => {
      const icon = L.divIcon({ className: '', html: `<div class="pin">${i + 1}</div>`, iconSize: [22, 22], iconAnchor: [11, 11] });
      const m = L.marker([p.lat, p.lng], { icon, draggable: true, title: p.name }).addTo(this.pins);
      m.on('dragend', () => { const ll = m.getLatLng(); onDrag(i, ll.lat, ll.lng); });
    });
  }
  showOption(origins, dest) {
    this.overlay.clearLayers();
    if (!dest) return;
    const pts = [[dest.lat, dest.lng]];
    L.circleMarker([dest.lat, dest.lng], { radius: 9, color: '#fff', weight: 2, fillColor: '#3ecf8e', fillOpacity: 1 }).bindTooltip(dest.label).addTo(this.overlay);
    for (const o of origins) {
      L.polyline([[o.lat, o.lng], [dest.lat, dest.lng]], { color: '#4f8cff', weight: 2, opacity: .7 }).addTo(this.overlay);
      pts.push([o.lat, o.lng]);
    }
    this.map.fitBounds(pts, { padding: [60, 60] });
  }
  fit(people) {
    if (!people.length) return;
    this.map.fitBounds(people.map((p) => [p.lat, p.lng]), { padding: [80, 80], maxZoom: 5 });
  }
}

export async function createMap(el, mapsKey, onClick) {
  if (mapsKey) {
    try {
      const v = new GoogleView();
      await v.init(el, mapsKey, onClick);
      return v;
    } catch { el.replaceChildren(); }
  }
  const v = new LeafletView();
  await v.init(el, '', onClick);
  return v;
}
