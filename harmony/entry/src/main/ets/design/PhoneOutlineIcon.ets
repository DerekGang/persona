import { Colors } from './Colors';

interface GlyphDrawing {
  name: string;
  width: number;
  height: number;
  path: string;
  pen: number;
}

const cube = 'M12 1.2 L21.5 6.6 L21.5 17.4 L12 22.8 L2.5 17.4 L2.5 6.6 Z M2.5 6.6 L12 12 L21.5 6.6 M12 12 L12 22.8';
const drawings: GlyphDrawing[] = [
  {name:'cube',width:24,height:24,path:cube,pen:1.5},
  {name:'package',width:24,height:24,path:cube+' M7.2 4 L16.8 9.4',pen:1.5},
  {name:'laptop',width:36,height:22,path:'M4.7 17.8 V4.4 Q4.7 3 6.2 3 H29.8 Q31.3 3 31.3 4.4 V17.8 Z M2.5 19 H33.5',pen:2.2},
  {name:'server',width:29,height:22,path:'M3.5 2 H25.5 Q26.5 2 26.5 3 V19 Q26.5 20 25.5 20 H3.5 Q2.5 20 2.5 19 V3 Q2.5 2 3.5 2 Z M2.5 8 H26.5 M2.5 14 H26.5 M21.5 5 H23 M21.5 11 H23 M21.5 17 H23',pen:2},
  {name:'desktop',width:30,height:24,path:'M3 1.8 H27 Q28 1.8 28 3 V18 H2 V3 Q2 1.8 3 1.8 Z M2 15 H28 M15 18 V21.8 M10 22 H20',pen:2},
  {name:'forward',width:9,height:14,path:'M1.7 1.7 L6.9 7 L1.7 12.3',pen:1.9},
];

// The SVG path commands and their pen use physical pixels within the viewport. The outer frame follows the interface size.
@ComponentV2
export struct PhoneOutlineIcon {
  @Param name: string = 'cube';
  @Param iconScale: number = 1;
  @Param color: ResourceColor = Colors.link;

  @Computed get drawing(): GlyphDrawing { return drawings.find((entry: GlyphDrawing) => entry.name === this.name) || drawings[0]; }

  build() {
    Shape() {
      Path().commands(this.drawing.path).fill(Color.Transparent).stroke(this.color)
        .strokeWidth(String(this.drawing.pen)+'px').strokeLineCap(LineCapStyle.Round).strokeLineJoin(LineJoinStyle.Round)
    }.viewPort({x:0,y:0,width:String(this.drawing.width)+'px',height:String(this.drawing.height)+'px'})
      .width(this.drawing.width*this.iconScale).height(this.drawing.height*this.iconScale)
      .accessibilityLevel('no')
  }
}
