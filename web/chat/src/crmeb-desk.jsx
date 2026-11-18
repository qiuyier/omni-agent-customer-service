import React from 'react';
import {createRoot} from 'react-dom/client';
import {Desk} from './desk.jsx';
import {createCRMEBDeskAPI,readStaffLogin} from './crmeb-desk.mjs';
import {eventID} from './crmeb.mjs';
const request=createCRMEBDeskAPI({getLogin:()=>readStaffLogin(document.cookie)});
createRoot(document.getElementById('root')).render(<Desk request={request} externalLoginURL="/kefu" customerURL="/omni-chat-test/" newEventID={eventID}/>);
