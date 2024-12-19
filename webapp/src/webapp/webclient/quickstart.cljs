(ns webapp.webclient.quickstart
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["lucide-react" :refer [PackageOpen]]
            ["@radix-ui/themes" :refer [Flex Box Text Heading Theme]]
            [re-frame.core :as rf]
            [webapp.components.button :as button]))

(defn main []
  (let [user (rf/subscribe [:users->current-user])]
    (fn []
      (if (-> @user :data :admin?)
      [:div {:class "h-full bg-editor flex flex-col items-center justify-center gap-6 py-12 px-28"}
       [:div {:class "h-64 w-full max-w-3xl bg-black bg-opacity-10 border border-dashed border-gray-700 rounded-xl flex flex-col items-center"}
        [:div {:class "flex flex-col items-center gap-2 py-6"}
         [:> hero-outline-icon/CircleStackIcon {:class "h-12 w-12 shrink-0 text-gray-300"
                                                :aria-hidden "true"}]
         [:span {:class "text-base text-gray-50 font-semibold"}
          "Start with a Demo setup"]
         [:span {:class "text-xs text-gray-50 text-center leading-6"}
          "Quickly add a demo PostgreSQL database"
          [:br]
          "to your organization"]]
        [button/primary {:text [:div {:class "flex items-center gap-small"}
                                [:span "Quick start"]
                                [:> hero-solid-icon/ArrowRightIcon {:class "h-6 w-6 text-white"
                                                                    :aria-hidden "true"}]]
                         :on-click #(rf/dispatch [:connections->quickstart-create-postgres-demo])}]]
       [:div {:class "flex w-full max-w-3xl gap-6"}
        [:div {:class "h-64 p-6 w-full bg-black bg-opacity-10 border border-dashed border-gray-700 rounded-xl flex flex-col gap-6 items-center"}
         [:div {:class "flex flex-col items-center gap-2"}
          [:> hero-outline-icon/ArrowRightEndOnRectangleIcon {:class "h-12 w-12 shrink-0 text-gray-300"
                                                              :aria-hidden "true"}]
          [:span {:class "text-base text-gray-50 font-semibold"}
           "Connect your database"]
          [:span {:class "h-12 text-xs text-gray-50 leading-6"}
           "Get started by creating a new connection"]]
         [button/black {:text [:div {:class "flex items-center gap-small"}
                               [:> hero-solid-icon/PlusIcon {:class "h-6 w-6 text-white"
                                                             :aria-hidden "true"}]
                               [:span "Add database"]]
                        :on-click #(rf/dispatch [:navigate :create-connection {:type "database"}])}]]
        [:div {:class "h-64 w-full bg-black bg-opacity-10 border border-dashed border-gray-700 rounded-xl flex flex-col items-center"}
         [:div {:class "flex flex-col items-center gap-2 py-6"}
          [:> hero-outline-icon/SquaresPlusIcon {:class "h-12 w-12 shrink-0 text-gray-300"
                                                 :aria-hidden "true"}]
          [:span {:class "text-base text-gray-50 font-semibold"}
           "Connect your service"]
          [:span {:class "h-12 text-xs text-gray-50 text-center leading-6"}
           "Add a remote server, rails app, nodejs"
           [:br]
           "or any other service"]]
         [button/black {:text [:div {:class "flex items-center gap-small"}
                               [:> hero-solid-icon/PlusIcon {:class "h-6 w-6 text-white"
                                                             :aria-hidden "true"}]
                               [:span "Add service"]]
                        :on-click #(rf/dispatch [:navigate :create-connection {:type "application"}])}]]]]
      [:div {:class "h-full bg-editor flex flex-col items-center justify-center gap-6 py-12 px-28"}
       [:div {:class "h-64 w-full max-w-3xl bg-black bg-opacity-10 border border-dashed border-gray-700 rounded-xl p-10"}
        [:> Theme {:appearance "dark"
                   :class "bg-transparent"}
         [:> Flex {:direction "column"
                   :gap "5"
                   :justify "center"
                   :align "center"}
          [:> Box
           [:> PackageOpen {:color "white"
                            :stroke-width "0.5px"
                            :size 72}]]
          [:> Box {:align "center"}
           [:> Heading {:size "4"
                        :as "h2"
                        :weight "semibold"}
            "You don't have access to any connections"]
           [:> Text {:size "2"}
            "Contact your organization administrator to get access to connections"]]]]]]))))
