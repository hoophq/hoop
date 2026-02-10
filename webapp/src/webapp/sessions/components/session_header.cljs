(ns webapp.sessions.components.session-header
  (:require
   ["@heroicons/react/24/outline" :as hero-outline-icon]
   ["@radix-ui/themes" :refer [Box Flex Text Heading Tooltip]]
   ["lucide-react" :refer [Share2 X]]
   ["clipboard" :as clipboardjs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.routes :as routes]))

(defn- re-run-session [session]
  (if (-> session :labels :runbookFile)
    (do
      (let [labels (:labels session)
            file-name (:runbookFile labels)
            params (js/JSON.parse (:runbookParameters labels))
            connection-name (:connection session)
            repository (:runbookRepository labels)
            on-success (fn [res]
                         (rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}])
                         (rf/dispatch [:audit->get-sessions]))
            on-failure (fn [_error-message error]
                         (rf/dispatch [:audit->get-session-by-id {:id (:session_id error) :verb "exec"}])
                         (rf/dispatch [:audit->get-sessions]))]
        (rf/dispatch [:runbooks/exec {:file-name file-name
                                      :params params
                                      :connection-name connection-name
                                      :repository repository
                                      :on-success on-success
                                      :on-failure on-failure}]))
      (rf/dispatch [:audit->clear-session-details-state {:status :loading}]))
    (do
      (rf/dispatch [:jira-integration->get])
      (rf/dispatch [:audit->re-run-session session]))))

(defn main [{:keys [session on-close clipboard-disabled?]}]
  (r/with-let [clipboard-url (when-not clipboard-disabled?
                               (new clipboardjs ".copy-session-link"))
               _ (when clipboard-url (.on clipboard-url "success" #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}])))]
    (let [can-rerun? (and (= (:verb session) "exec")
                          (nil? (-> session :integrations_metadata :jira_issue_url)))]
      [:> Box {:class "mb-5"}
       [:> Flex {:justify "between" :align "start"}
        ;; Left side - Title and role
        [:> Heading {:as "h2" :size "5" :weight "bold" :class "text-gray-12"}
         "Session Details"]

        ;; Right side - Action buttons
        [:> Flex {:gap "2" :align "center"}
         ;; Re-run button
         (when can-rerun?
           [:> Tooltip {:content "Re-run session"}
            [:div {:class "rounded-full p-2 bg-gray-3 hover:bg-gray-4 transition cursor-pointer"
                   :on-click #(re-run-session session)}
             [:> hero-outline-icon/PlayIcon {:class "h-5 w-5 text-gray-11"}]]])

         ;; Share button (copy link)
         (when-not clipboard-disabled?
           [:> Tooltip {:content "Copy link"}
            [:div {:class "rounded-full p-2 bg-gray-3 hover:bg-gray-4 transition cursor-pointer copy-session-link"
                   :data-clipboard-text (str (-> js/document .-location .-origin)
                                             (routes/url-for :sessions)
                                             "/" (:id session))}
             [:> Share2 {:size 20 :class "text-gray-11"}]]])

         ;; Close button
         [:> Tooltip {:content "Close"}
          [:div {:class "rounded-full p-2 bg-gray-3 hover:bg-gray-4 transition cursor-pointer"
                 :on-click on-close}
           [:> X {:size 20 :class "text-gray-11"}]]]]]])

    (finally
      (when clipboard-url
        (.destroy clipboard-url)))))
