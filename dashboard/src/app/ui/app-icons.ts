import {
  mdiCameraIris,
  mdiCloud,
  mdiFileExcelOutline,
  mdiFilePdfBox,
  mdiFileWordOutline,
  mdiImageEditOutline,
  mdiImageMultiple,
  mdiKey,
  mdiMovieEditOutline,
  mdiMovieOpenStarOutline,
  mdiPageLayoutBody,
  mdiPresentation,
  mdiRulerSquare,
  mdiTelevisionPlay,
  mdiVectorCurve,
  mdiViewDashboardOutline,
  mdiWaveform,
} from '@mdi/js';

/**
 * The Material Design Icons the catalog manifests name (`icon: mdi:<name>`),
 * and only those, so only their paths are bundled. Bundled, never fetched:
 * the appliance works offline. A test fails when a manifest names an icon
 * missing here; add the import and the entry together.
 */
export const APP_ICONS: Readonly<Record<string, string>> = {
  'camera-iris': mdiCameraIris,
  'cloud': mdiCloud,
  'file-excel-outline': mdiFileExcelOutline,
  'file-pdf-box': mdiFilePdfBox,
  'file-word-outline': mdiFileWordOutline,
  'image-edit-outline': mdiImageEditOutline,
  'image-multiple': mdiImageMultiple,
  'key': mdiKey,
  'movie-edit-outline': mdiMovieEditOutline,
  'movie-open-star-outline': mdiMovieOpenStarOutline,
  'page-layout-body': mdiPageLayoutBody,
  'presentation': mdiPresentation,
  'ruler-square': mdiRulerSquare,
  'television-play': mdiTelevisionPlay,
  'vector-curve': mdiVectorCurve,
  'view-dashboard-outline': mdiViewDashboardOutline,
  'waveform': mdiWaveform,
};
