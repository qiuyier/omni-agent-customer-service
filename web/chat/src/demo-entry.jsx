import React from 'react';
import {createRoot} from 'react-dom/client';
import {Home,Chat} from './app.jsx';
import {Desk} from './desk.jsx';
createRoot(document.getElementById('root')).render(location.pathname==='/desk/'?<Desk/>:location.pathname==='/chat/'||location.pathname==='/chat'?<Chat/>:<Home/>);
